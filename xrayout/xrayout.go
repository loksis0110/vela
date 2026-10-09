// Package xrayout adds an "xray" outbound type to sing-box. It runs servers
// that sing-box itself can't (XHTTP, mKCP, VLESS encryption, full Xray JSON
// configs) on an embedded Xray-core, so they work like any other server:
// in the selector, in automatic selection, in latency checks.
//
// Options:
//
//	{"type": "xray", "tag": "...", "outbounds": [<Xray outbound>, ...]}
//
// The first Xray outbound is the server; the others are helpers it refers to
// (dialerProxy / proxySettings chains, e.g. a "fragment" freedom outbound).
// Xray's own connections are made with sing-box's dialer, so they bypass the
// tunnel and use sing-box DNS exactly like built-in outbounds do.
package xrayout

import (
	"context"
	"net"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/adapter/outbound"
	"github.com/sagernet/sing-box/common/dialer"
	"github.com/sagernet/sing-box/include"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	E "github.com/sagernet/sing/common/exceptions"
	"github.com/sagernet/sing/common/json"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	xnet "github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/common/session"
	xcore "github.com/xtls/xray-core/core"
)

// Type is the sing-box outbound type name.
const Type = "xray"

// Options of the "xray" outbound.
type Options struct {
	option.DialerOptions
	Outbounds []json.RawMessage `json:"outbounds"`
}

func init() {
	include.ExtraOutbounds = append(include.ExtraOutbounds, func(registry *outbound.Registry) {
		outbound.Register[Options](registry, Type, NewOutbound)
	})
}

var (
	_ adapter.Outbound  = (*Outbound)(nil)
	_ adapter.Lifecycle = (*Outbound)(nil)
)

// Outbound sends connections through an Xray outbound handler.
type Outbound struct {
	outbound.Adapter
	logger  log.ContextLogger
	dialer  N.Dialer
	key     string
	mainTag string
	handler *handlerSet
}

// NewOutbound creates the outbound; the Xray handlers are added on Start.
func NewOutbound(ctx context.Context, router adapter.Router, logger log.ContextLogger, tag string, options Options) (adapter.Outbound, error) {
	if len(options.Outbounds) == 0 {
		return nil, E.New("xray: no outbounds")
	}
	outboundDialer, err := dialer.New(ctx, options.DialerOptions, true)
	if err != nil {
		return nil, err
	}
	key := globalHub.newKey()
	set, err := buildHandlers(key, options.Outbounds)
	if err != nil {
		return nil, E.Cause(err, "xray")
	}
	return &Outbound{
		Adapter: outbound.NewAdapterWithDialerOptions(Type, tag, []string{N.NetworkTCP, N.NetworkUDP}, options.DialerOptions),
		logger:  logger,
		dialer:  outboundDialer,
		key:     key,
		mainTag: set.mainTag,
		handler: set,
	}, nil
}

// Start adds the Xray handlers to the shared Xray instance.
func (h *Outbound) Start(stage adapter.StartStage) error {
	if stage != adapter.StartStateStart {
		return nil
	}
	return globalHub.add(h.key, h.dialer, h.logger, h.handler)
}

// Close removes the handlers again.
func (h *Outbound) Close() error {
	globalHub.remove(h.key, h.handler)
	return nil
}

func (h *Outbound) xrayContext(ctx context.Context) (context.Context, *xcore.Instance, error) {
	instance := globalHub.instanceFor(h.key)
	if instance == nil {
		return nil, nil, E.New("xray: outbound is not started")
	}
	// Xray keeps the connection alive as long as its context: detach it from
	// short dial deadlines, sing-box closes the connection when it is done.
	xctx := context.WithValue(context.WithoutCancel(ctx), xcore.XrayKey(1), instance)
	content := new(session.Content)
	content.SkipDNSResolve = true
	xctx = session.ContextWithContent(xctx, content)
	xctx = session.SetForcedOutboundTagToContext(xctx, h.mainTag)
	return xctx, instance, nil
}

// DialContext opens a TCP connection (or a connected UDP one) through Xray.
func (h *Outbound) DialContext(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
	ctx, metadata := adapter.ExtendContext(ctx)
	metadata.Outbound = h.Tag()
	metadata.Destination = destination
	switch N.NetworkName(network) {
	case N.NetworkTCP:
		h.logger.InfoContext(ctx, "outbound connection to ", destination)
		xctx, instance, err := h.xrayContext(ctx)
		if err != nil {
			return nil, err
		}
		return xcore.Dial(xctx, instance, xnet.TCPDestination(toXrayAddress(destination), xnet.Port(destination.Port)))
	case N.NetworkUDP:
		h.logger.InfoContext(ctx, "outbound packet connection to ", destination)
		packetConn, err := h.listenPacket(ctx)
		if err != nil {
			return nil, err
		}
		return &boundPacketConn{packetConn: packetConn, destination: destination}, nil
	}
	return nil, E.Extend(N.ErrUnknownNetwork, network)
}

// ListenPacket opens a UDP association through Xray.
func (h *Outbound) ListenPacket(ctx context.Context, destination M.Socksaddr) (net.PacketConn, error) {
	ctx, metadata := adapter.ExtendContext(ctx)
	metadata.Outbound = h.Tag()
	metadata.Destination = destination
	h.logger.InfoContext(ctx, "outbound packet connection to ", destination)
	return h.listenPacket(ctx)
}

func (h *Outbound) listenPacket(ctx context.Context) (*packetConn, error) {
	xctx, instance, err := h.xrayContext(ctx)
	if err != nil {
		return nil, err
	}
	return newPacketConn(xctx, instance)
}

func toXrayAddress(destination M.Socksaddr) xnet.Address {
	if destination.IsFqdn() {
		return xnet.DomainAddress(destination.Fqdn)
	}
	return xnet.IPAddress(destination.Addr.Unmap().AsSlice())
}
