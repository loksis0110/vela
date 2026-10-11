package xrayout

import (
	"reflect"
	"sync"
	_ "unsafe" // go:linkname

	xcommon "github.com/xtls/xray-core/common"
	xnet "github.com/xtls/xray-core/common/net"
	xoutbound "github.com/xtls/xray-core/features/outbound"
	"github.com/xtls/xray-core/transport/internet"
	"github.com/xtls/xray-core/transport/internet/splithttp"
	"google.golang.org/grpc"
)

// Xray keeps XHTTP and gRPC clients (with their connections) in global maps
// keyed by the stream settings of the handler that dialed them, and never
// removes them. Every connection, reconnection and latency check creates new
// handlers, so without cleanup those clients pile up, keep connections to
// servers open (HTTP/3 and gRPC even reconnect on their own) and are reused
// after a network change although their sockets are dead. The maps are
// reached with go:linkname; dialerConf mirrors Xray's key type exactly.

type dialerConf struct {
	xnet.Destination
	*internet.MemoryStreamConfig
}

//go:linkname xhttpDialers github.com/xtls/xray-core/transport/internet/splithttp.globalDialerMap
var xhttpDialers map[dialerConf]*splithttp.XmuxManager

//go:linkname xhttpDialersMu github.com/xtls/xray-core/transport/internet/splithttp.globalDialerAccess
var xhttpDialersMu sync.Mutex

//go:linkname grpcDialers github.com/xtls/xray-core/transport/internet/grpc.globalDialerMap
var grpcDialers map[dialerConf]*grpc.ClientConn

//go:linkname grpcDialersMu github.com/xtls/xray-core/transport/internet/grpc.globalDialerAccess
var grpcDialersMu sync.Mutex

var memoryStreamConfigType = reflect.TypeOf((*internet.MemoryStreamConfig)(nil))

// handlerStreamSettings returns the stream settings of an Xray outbound
// handler (proxyman's Handler keeps them in an unexported field).
func handlerStreamSettings(h xoutbound.Handler) *internet.MemoryStreamConfig {
	v := reflect.ValueOf(h)
	if v.Kind() != reflect.Pointer || v.IsNil() || v.Elem().Kind() != reflect.Struct {
		return nil
	}
	f := v.Elem().FieldByName("streamSettings")
	if !f.IsValid() || f.Type() != memoryStreamConfigType || f.IsNil() {
		return nil
	}
	return (*internet.MemoryStreamConfig)(f.UnsafePointer())
}

// purgeTransportClients forgets (and closes where Xray allows it) the XHTTP
// and gRPC clients created for the given stream settings.
func purgeTransportClients(settings []*internet.MemoryStreamConfig) {
	match := map[*internet.MemoryStreamConfig]bool{}
	for _, s := range settings {
		if s != nil {
			match[s] = true
			if s.DownloadSettings != nil {
				match[s.DownloadSettings] = true
			}
		}
	}
	if len(match) == 0 {
		return
	}
	var managers []*splithttp.XmuxManager
	xhttpDialersMu.Lock()
	for k, m := range xhttpDialers {
		if match[k.MemoryStreamConfig] {
			delete(xhttpDialers, k)
			managers = append(managers, m)
		}
	}
	xhttpDialersMu.Unlock()
	var conns []*grpc.ClientConn
	grpcDialersMu.Lock()
	for k, c := range grpcDialers {
		if match[k.MemoryStreamConfig] {
			delete(grpcDialers, k)
			conns = append(conns, c)
		}
	}
	grpcDialersMu.Unlock()
	// nobody can reach these clients any more: close them outside the locks
	for _, m := range managers {
		closeXmuxManager(m)
	}
	for _, c := range conns {
		if c != nil {
			_ = c.Close()
		}
	}
}

// closeXmuxManager closes the clients of an XHTTP connection pool. Only
// HTTP/3 clients need it (HTTP/1.1 and HTTP/2 connections close when idle),
// but QUIC keepalives would hold those open forever.
func closeXmuxManager(m *splithttp.XmuxManager) {
	if m == nil {
		return
	}
	f := reflect.ValueOf(m).Elem().FieldByName("xmuxClients")
	if !f.IsValid() || f.Kind() != reflect.Slice || f.Type().Elem() != reflect.TypeOf((*splithttp.XmuxClient)(nil)) {
		return
	}
	for i := 0; i < f.Len(); i++ {
		item := f.Index(i)
		if item.IsNil() {
			continue
		}
		client := (*splithttp.XmuxClient)(item.UnsafePointer())
		if client.XmuxConn != nil {
			_ = xcommon.Close(client.XmuxConn)
		}
	}
}

// cachedTransportClients counts the clients in Xray's global maps (tests).
func cachedTransportClients() int {
	xhttpDialersMu.Lock()
	n := len(xhttpDialers)
	xhttpDialersMu.Unlock()
	grpcDialersMu.Lock()
	n += len(grpcDialers)
	grpcDialersMu.Unlock()
	return n
}
