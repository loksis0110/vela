package xrayout

import (
	"context"
	stdjson "encoding/json"
	"fmt"
	"net"
	"net/netip"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sagernet/sing-box/log"
	E "github.com/sagernet/sing/common/exceptions"
	"github.com/sagernet/sing/common/json"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	"github.com/xtls/xray-core/app/dispatcher"
	"github.com/xtls/xray-core/app/proxyman"
	xcommon "github.com/xtls/xray-core/common"
	xlog "github.com/xtls/xray-core/common/log"
	xnet "github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/common/serial"
	"github.com/xtls/xray-core/common/session"
	xcore "github.com/xtls/xray-core/core"
	xoutbound "github.com/xtls/xray-core/features/outbound"
	"github.com/xtls/xray-core/infra/conf"
	"github.com/xtls/xray-core/transport/internet"

	// Xray features and protocols used by client configs.
	_ "github.com/xtls/xray-core/app/proxyman/inbound"
	_ "github.com/xtls/xray-core/app/proxyman/outbound"
	_ "github.com/xtls/xray-core/proxy/blackhole"
	_ "github.com/xtls/xray-core/proxy/freedom"
	_ "github.com/xtls/xray-core/proxy/http"
	_ "github.com/xtls/xray-core/proxy/shadowsocks"
	_ "github.com/xtls/xray-core/proxy/socks"
	_ "github.com/xtls/xray-core/proxy/trojan"
	_ "github.com/xtls/xray-core/proxy/vless/outbound"
	_ "github.com/xtls/xray-core/proxy/vmess/outbound"
	_ "github.com/xtls/xray-core/transport/internet/grpc"
	_ "github.com/xtls/xray-core/transport/internet/headers/http"
	_ "github.com/xtls/xray-core/transport/internet/headers/noop"
	_ "github.com/xtls/xray-core/transport/internet/httpupgrade"
	_ "github.com/xtls/xray-core/transport/internet/kcp"
	_ "github.com/xtls/xray-core/transport/internet/reality"
	_ "github.com/xtls/xray-core/transport/internet/splithttp"
	_ "github.com/xtls/xray-core/transport/internet/tagged/taggedimpl"
	_ "github.com/xtls/xray-core/transport/internet/tcp"
	_ "github.com/xtls/xray-core/transport/internet/tls"
	_ "github.com/xtls/xray-core/transport/internet/udp"
	_ "github.com/xtls/xray-core/transport/internet/websocket"
)

// hub is the one Xray instance of the process. Every sing-box "xray"
// outbound adds its handlers to it under unique tags ("v12/main"), so
// several sing-box instances (the connection and a latency check) can use
// Xray at the same time: Xray keeps some state in globals, one instance
// keeps that state consistent.
type hub struct {
	mu       sync.Mutex
	instance *xcore.Instance
	counter  atomic.Uint64
	entries  map[string]*entry
	// last receives Xray's log messages and dials that can't be matched to
	// an outbound. Atomic: Xray logs while the hub is locked (instance
	// start, handler changes), so the log handler must never take mu.
	last atomic.Pointer[entry]
}

type entry struct {
	dialer N.Dialer
	logger log.ContextLogger
}

var globalHub = &hub{entries: map[string]*entry{}}

func (h *hub) newKey() string {
	return fmt.Sprintf("v%d", h.counter.Add(1))
}

func (h *hub) ensure() (*xcore.Instance, error) {
	if h.instance != nil {
		return h.instance, nil
	}
	config := &xcore.Config{App: []*serial.TypedMessage{
		serial.ToTypedMessage(&dispatcher.Config{}),
		serial.ToTypedMessage(&proxyman.InboundConfig{}),
		serial.ToTypedMessage(&proxyman.OutboundConfig{}),
	}}
	xlog.RegisterHandler(logHandler{})
	instance, err := xcore.New(config)
	if err != nil {
		return nil, err
	}
	if err := instance.Start(); err != nil {
		_ = instance.Close()
		return nil, err
	}
	internet.UseAlternativeSystemDialer(systemDialer{})
	h.instance = instance
	return instance, nil
}

func (h *hub) add(key string, d N.Dialer, logger log.ContextLogger, set *handlerSet) error {
	h.mu.Lock()
	instance, err := h.ensure()
	if err != nil {
		h.mu.Unlock()
		return E.Cause(err, "start xray")
	}
	e := &entry{dialer: d, logger: logger}
	h.entries[key] = e
	h.last.Store(e)
	for i, cfg := range set.configs {
		if err := addHandler(instance, cfg); err != nil {
			settings := h.removeLocked(key, set.tags[:i])
			h.mu.Unlock()
			purgeTransportClients(settings)
			return E.Cause(err, "xray outbound")
		}
	}
	h.mu.Unlock()
	return nil
}

// addHandler guards against panics in Xray while it creates a handler.
func addHandler(instance *xcore.Instance, cfg *xcore.OutboundHandlerConfig) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = E.New("panic: ", r)
		}
	}()
	return xcore.AddOutboundHandler(instance, cfg)
}

func (h *hub) remove(key string, set *handlerSet) {
	h.mu.Lock()
	settings := h.removeLocked(key, set.tags)
	h.mu.Unlock()
	purgeTransportClients(settings)
	if len(settings) > 0 {
		// dials that were already under way create their clients a moment
		// later: clean up once more after them
		for _, d := range []time.Duration{3 * time.Second, 30 * time.Second} {
			time.AfterFunc(d, func() { purgeTransportClients(settings) })
		}
	}
}

// removeLocked removes the handlers and returns their stream settings, so
// the transport clients they created can be dropped (outside the lock).
func (h *hub) removeLocked(key string, tags []string) []*internet.MemoryStreamConfig {
	var settings []*internet.MemoryStreamConfig
	if manager := h.manager(); manager != nil {
		for _, tag := range tags {
			handler := manager.GetHandler(tag)
			if handler == nil {
				continue
			}
			settings = append(settings, handlerStreamSettings(handler))
			_ = manager.RemoveHandler(context.Background(), tag)
			_ = xcommon.Close(handler)
		}
	}
	if e := h.entries[key]; e != nil {
		h.last.CompareAndSwap(e, nil)
	}
	delete(h.entries, key)
	if h.last.Load() == nil {
		for _, e := range h.entries {
			h.last.Store(e)
			break
		}
	}
	return settings
}

func (h *hub) manager() xoutbound.Manager {
	if h.instance == nil {
		return nil
	}
	manager, _ := h.instance.GetFeature(xoutbound.ManagerType()).(xoutbound.Manager)
	return manager
}

// resetTransports drops the pooled transport connections (XHTTP, gRPC) of
// a set of handlers, e.g. after a network change made their sockets dead.
func (h *hub) resetTransports(set *handlerSet) {
	var settings []*internet.MemoryStreamConfig
	h.mu.Lock()
	if manager := h.manager(); manager != nil {
		for _, tag := range set.tags {
			if handler := manager.GetHandler(tag); handler != nil {
				settings = append(settings, handlerStreamSettings(handler))
			}
		}
	}
	h.mu.Unlock()
	purgeTransportClients(settings)
}

// Stats describes the shared Xray instance (used by tests to find leaks).
type Stats struct {
	// Outbounds is the number of sing-box "xray" outbounds started.
	Outbounds int
	// Handlers is the number of Xray outbound handlers.
	Handlers int
	// TransportClients counts the pooled XHTTP and gRPC clients.
	TransportClients int
}

// CurrentStats returns the current numbers.
func CurrentStats() Stats {
	h := globalHub
	h.mu.Lock()
	s := Stats{Outbounds: len(h.entries)}
	if manager := h.manager(); manager != nil {
		s.Handlers = len(manager.ListHandlers(context.Background()))
	}
	h.mu.Unlock()
	s.TransportClients = cachedTransportClients()
	return s
}

// Check reports whether the Xray engine accepts these outbounds (the first
// one is the server, the others its helpers), so a server with a broken
// config can be rejected when it is imported instead of failing the whole
// connection later.
func Check(outbounds []any) error {
	raws := make([]json.RawMessage, 0, len(outbounds))
	for _, o := range outbounds {
		data, err := stdjson.Marshal(o)
		if err != nil {
			return err
		}
		raws = append(raws, data)
	}
	if len(raws) == 0 {
		return E.New("xray: no outbounds")
	}
	_, err := buildHandlers("check", raws)
	return err
}

func (h *hub) instanceFor(key string) *xcore.Instance {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.entries[key] == nil {
		return nil
	}
	return h.instance
}

// entryFor finds the sing-box outbound a connection belongs to by the tag of
// the Xray handler that dials it.
func (h *hub) entryFor(ctx context.Context) *entry {
	h.mu.Lock()
	defer h.mu.Unlock()
	outbounds := session.OutboundsFromContext(ctx)
	for i := len(outbounds) - 1; i >= 0; i-- {
		if key, _, ok := strings.Cut(outbounds[i].Tag, "/"); ok {
			if e := h.entries[key]; e != nil {
				return e
			}
		}
	}
	return h.last.Load()
}

// systemDialer makes Xray's network connections with sing-box's dialer:
// they leave through the physical interface (not the tunnel), are protected
// on Android and resolve server names with sing-box DNS.
type systemDialer struct{}

func (systemDialer) Dial(ctx context.Context, _ xnet.Address, dest xnet.Destination, _ *internet.SocketConfig) (net.Conn, error) {
	e := globalHub.entryFor(ctx)
	if e == nil {
		return nil, E.New("xray: no dialer")
	}
	var addr M.Socksaddr
	if dest.Address.Family().IsDomain() {
		addr = M.Socksaddr{Fqdn: dest.Address.Domain(), Port: uint16(dest.Port)}
	} else {
		ip, _ := netip.AddrFromSlice(dest.Address.IP())
		addr = M.SocksaddrFrom(ip.Unmap(), uint16(dest.Port))
	}
	if dest.Network == xnet.Network_UDP {
		conn, err := e.dialer.DialContext(ctx, N.NetworkUDP, addr)
		if err != nil {
			return nil, err
		}
		return &internet.PacketConnWrapper{PacketConn: &connPacket{Conn: conn}, Dest: conn.RemoteAddr()}, nil
	}
	return e.dialer.DialContext(ctx, N.NetworkTCP, addr)
}

func (systemDialer) DestIpAddress() net.IP { return nil }

// connPacket turns a connected UDP socket into the PacketConn Xray expects.
type connPacket struct {
	net.Conn
}

func (c *connPacket) ReadFrom(p []byte) (int, net.Addr, error) {
	n, err := c.Conn.Read(p)
	return n, c.Conn.RemoteAddr(), err
}

func (c *connPacket) WriteTo(p []byte, _ net.Addr) (int, error) {
	return c.Conn.Write(p)
}

var debugLog = os.Getenv("VELA_XRAY_DEBUG") != ""

// logHandler forwards Xray warnings and errors to the sing-box log.
type logHandler struct{}

func (logHandler) Handle(msg xlog.Message) {
	if debugLog {
		fmt.Fprintln(os.Stderr, "xray:", msg.String())
	}
	m, ok := msg.(*xlog.GeneralMessage)
	if !ok || m.Severity > xlog.Severity_Warning {
		return
	}
	e := globalHub.last.Load()
	if e == nil || e.logger == nil {
		return
	}
	if m.Severity == xlog.Severity_Error {
		e.logger.Error("xray: ", m.Content)
	} else {
		e.logger.Warn("xray: ", m.Content)
	}
}

// handlerSet are the Xray handlers of one sing-box outbound.
type handlerSet struct {
	mainTag string
	tags    []string
	configs []*xcore.OutboundHandlerConfig
}

// buildHandlers renames the tags of the given Xray outbounds into the key's
// namespace, keeps the first outbound and the helpers it refers to, and
// builds Xray handler configs.
func buildHandlers(key string, raws []json.RawMessage) (*handlerSet, error) {
	type item struct {
		obj map[string]any
		tag string
	}
	items := make([]item, 0, len(raws))
	byTag := map[string]int{}
	for i, raw := range raws {
		var obj map[string]any
		if err := stdjson.Unmarshal(raw, &obj); err != nil {
			return nil, E.Cause(err, "outbound ", i)
		}
		tag, _ := obj["tag"].(string)
		if tag == "" {
			tag = fmt.Sprintf("o%d", i)
		}
		if _, dup := byTag[tag]; !dup {
			byTag[tag] = i
		}
		items = append(items, item{obj: obj, tag: tag})
	}
	rename := func(tag string) string { return key + "/" + tag }
	refs := func(obj map[string]any) []string {
		var out []string
		if ss, ok := obj["streamSettings"].(map[string]any); ok {
			if so, ok := ss["sockopt"].(map[string]any); ok {
				if t, ok := so["dialerProxy"].(string); ok && t != "" {
					out = append(out, t)
				}
			}
		}
		if ps, ok := obj["proxySettings"].(map[string]any); ok {
			if t, ok := ps["tag"].(string); ok && t != "" {
				out = append(out, t)
			}
		}
		return out
	}
	// the first outbound and everything reachable from it
	keep := []int{0}
	seen := map[int]bool{0: true}
	for i := 0; i < len(keep); i++ {
		for _, ref := range refs(items[keep[i]].obj) {
			j, ok := byTag[ref]
			if !ok {
				return nil, E.New("outbound refers to unknown tag ", ref)
			}
			if !seen[j] {
				seen[j] = true
				keep = append(keep, j)
			}
		}
	}
	set := &handlerSet{mainTag: rename(items[0].tag)}
	for _, idx := range keep {
		obj := items[idx].obj
		obj["tag"] = rename(items[idx].tag)
		if ss, ok := obj["streamSettings"].(map[string]any); ok {
			if so, ok := ss["sockopt"].(map[string]any); ok {
				if t, ok := so["dialerProxy"].(string); ok && t != "" {
					so["dialerProxy"] = rename(t)
				}
			}
		}
		if ps, ok := obj["proxySettings"].(map[string]any); ok {
			if t, ok := ps["tag"].(string); ok && t != "" {
				ps["tag"] = rename(t)
			}
		}
		data, err := stdjson.Marshal(obj)
		if err != nil {
			return nil, err
		}
		var detour conf.OutboundDetourConfig
		if err := stdjson.Unmarshal(data, &detour); err != nil {
			return nil, E.Cause(err, "outbound ", items[idx].tag)
		}
		built, err := safeBuild(&detour)
		if err != nil {
			return nil, E.Cause(err, "outbound ", items[idx].tag)
		}
		set.tags = append(set.tags, built.Tag)
		set.configs = append(set.configs, built)
	}
	return set, nil
}

// safeBuild guards against panics in Xray's config parser on malformed
// input: a bad server must not take the whole app down.
func safeBuild(detour *conf.OutboundDetourConfig) (config *xcore.OutboundHandlerConfig, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = E.New("invalid config: ", r)
		}
	}()
	return detour.Build()
}
