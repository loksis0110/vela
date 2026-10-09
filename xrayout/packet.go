package xrayout

import (
	"context"
	"net"
	"net/netip"
	"os"
	"sync"
	"time"

	E "github.com/sagernet/sing/common/exceptions"
	M "github.com/sagernet/sing/common/metadata"
	xbuf "github.com/xtls/xray-core/common/buf"
	xnet "github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/common/protocol/udp"
	xcore "github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/features/routing"
	xudp "github.com/xtls/xray-core/transport/internet/udp"
)

// packetConn is a UDP association through an Xray handler. Unlike Xray's own
// DialUDP it accepts domain names as destinations.
type packetConn struct {
	ctx        context.Context
	dispatcher *xudp.Dispatcher
	cache      chan *udp.Packet
	done       chan struct{}
	closeOnce  sync.Once

	deadlineMu sync.Mutex
	deadline   time.Time
	wake       chan struct{}
}

func newPacketConn(ctx context.Context, instance *xcore.Instance) (*packetConn, error) {
	d, ok := instance.GetFeature(routing.DispatcherType()).(routing.Dispatcher)
	if !ok {
		return nil, E.New("xray: no dispatcher")
	}
	c := &packetConn{
		ctx:   ctx,
		cache: make(chan *udp.Packet, 64),
		done:  make(chan struct{}),
		wake:  make(chan struct{}, 1),
	}
	c.dispatcher = xudp.NewDispatcher(d, c.callback)
	return c, nil
}

func (c *packetConn) callback(_ context.Context, packet *udp.Packet) {
	select {
	case <-c.done:
		packet.Payload.Release()
	case c.cache <- packet:
	default:
		packet.Payload.Release()
	}
}

func (c *packetConn) ReadFrom(p []byte) (int, net.Addr, error) {
	for {
		c.deadlineMu.Lock()
		deadline := c.deadline
		c.deadlineMu.Unlock()
		var (
			timer   *time.Timer
			timeout <-chan time.Time
		)
		if !deadline.IsZero() {
			d := time.Until(deadline)
			if d <= 0 {
				return 0, nil, os.ErrDeadlineExceeded
			}
			timer = time.NewTimer(d)
			timeout = timer.C
		}
		stop := func() {
			if timer != nil {
				timer.Stop()
			}
		}
		select {
		case packet := <-c.cache:
			stop()
			n := copy(p, packet.Payload.Bytes())
			packet.Payload.Release()
			return n, fromXrayDestination(packet.Source), nil
		case <-c.done:
			stop()
			return 0, nil, net.ErrClosed
		case <-timeout:
			return 0, nil, os.ErrDeadlineExceeded
		case <-c.wake:
			stop() // deadline changed
		}
	}
}

func (c *packetConn) WriteTo(p []byte, addr net.Addr) (int, error) {
	select {
	case <-c.done:
		return 0, net.ErrClosed
	default:
	}
	destination, err := toXrayUDPDestination(addr)
	if err != nil {
		return 0, err
	}
	buffer := xbuf.New()
	n, _ := buffer.Write(p)
	buffer.UDP = &destination
	c.dispatcher.Dispatch(c.ctx, destination, buffer)
	return n, nil
}

func (c *packetConn) Close() error {
	c.closeOnce.Do(func() {
		close(c.done)
		c.dispatcher.RemoveRay()
	})
	return nil
}

func (c *packetConn) LocalAddr() net.Addr {
	return &net.UDPAddr{IP: net.IPv4zero, Port: 0}
}

func (c *packetConn) SetDeadline(t time.Time) error { return c.SetReadDeadline(t) }

func (c *packetConn) SetReadDeadline(t time.Time) error {
	c.deadlineMu.Lock()
	c.deadline = t
	c.deadlineMu.Unlock()
	select {
	case c.wake <- struct{}{}:
	default:
	}
	return nil
}

func (c *packetConn) SetWriteDeadline(time.Time) error { return nil }

func toXrayUDPDestination(addr net.Addr) (xnet.Destination, error) {
	switch a := addr.(type) {
	case M.Socksaddr:
		return xnet.UDPDestination(toXrayAddress(a), xnet.Port(a.Port)), nil
	case *M.Socksaddr:
		return xnet.UDPDestination(toXrayAddress(*a), xnet.Port(a.Port)), nil
	case *net.UDPAddr:
		return xnet.UDPDestination(xnet.IPAddress(a.IP), xnet.Port(a.Port)), nil
	}
	s := M.ParseSocksaddr(addr.String())
	if !s.IsValid() {
		return xnet.Destination{}, E.New("xray: bad UDP address ", addr)
	}
	return xnet.UDPDestination(toXrayAddress(s), xnet.Port(s.Port)), nil
}

func fromXrayDestination(d xnet.Destination) net.Addr {
	if d.Address.Family().IsDomain() {
		return M.Socksaddr{Fqdn: d.Address.Domain(), Port: uint16(d.Port)}
	}
	ip, _ := netip.AddrFromSlice(d.Address.IP())
	return &net.UDPAddr{IP: ip.Unmap().AsSlice(), Port: int(d.Port)}
}

// boundPacketConn is a "connected" UDP connection to one destination.
type boundPacketConn struct {
	packetConn  *packetConn
	destination M.Socksaddr
}

func (c *boundPacketConn) Read(p []byte) (int, error) {
	n, _, err := c.packetConn.ReadFrom(p)
	return n, err
}

func (c *boundPacketConn) Write(p []byte) (int, error) {
	return c.packetConn.WriteTo(p, c.destination)
}

func (c *boundPacketConn) Close() error                       { return c.packetConn.Close() }
func (c *boundPacketConn) LocalAddr() net.Addr                { return c.packetConn.LocalAddr() }
func (c *boundPacketConn) RemoteAddr() net.Addr               { return c.destination.UDPAddr() }
func (c *boundPacketConn) SetDeadline(t time.Time) error      { return c.packetConn.SetDeadline(t) }
func (c *boundPacketConn) SetReadDeadline(t time.Time) error  { return c.packetConn.SetReadDeadline(t) }
func (c *boundPacketConn) SetWriteDeadline(t time.Time) error { return nil }
