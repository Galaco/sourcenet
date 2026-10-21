package client

import (
	"errors"
	"net"
	"sync"
)

// ErrTransportClosed is returned by Send after Close.
var ErrTransportClosed = errors.New("client: transport closed")

// Transport moves datagrams between the client and one server. Poll must not
// block; the client drains it from Update on the caller's goroutine.
type Transport interface {
	// Send transmits one datagram.
	Send(datagram []byte) error
	// Poll returns the next received datagram, or ok=false when none is
	// waiting.
	Poll() (datagram []byte, ok bool)
	// RemoteAddr returns the server address, used for Steam auth tickets.
	RemoteAddr() *net.UDPAddr
	Close() error
}

// UDPTransport is a Transport over a connected UDP socket. A goroutine reads
// datagrams into a bounded queue; when the queue is full new datagrams are
// dropped, as an overflowing socket buffer would.
type UDPTransport struct {
	conn  *net.UDPConn
	queue chan []byte
	once  sync.Once
	done  chan struct{}
	err   error
	mu    sync.Mutex
}

// DialUDP connects a UDP socket to a server ("host:port").
func DialUDP(addr string) (*UDPTransport, error) {
	raddr, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		return nil, err
	}
	conn, err := net.DialUDP("udp", nil, raddr)
	if err != nil {
		return nil, err
	}
	// Large reliable bursts (signon data, file transfers) arrive as many
	// split packets at once; give the socket room for them.
	_ = conn.SetReadBuffer(1 << 20)
	t := &UDPTransport{conn: conn, queue: make(chan []byte, 2048), done: make(chan struct{})}
	go t.readLoop()
	return t, nil
}

func (t *UDPTransport) readLoop() {
	buf := make([]byte, 65536)
	failures := 0
	for {
		n, err := t.conn.Read(buf)
		if err != nil {
			select {
			case <-t.done:
				return
			default:
			}
			// ICMP port unreachable surfaces as a read error on connected
			// UDP sockets; keep reading so a restarting server can be
			// reached, but give up on persistent failures.
			failures++
			if errors.Is(err, net.ErrClosed) || failures > 100 {
				t.mu.Lock()
				t.err = err
				t.mu.Unlock()
				return
			}
			continue
		}
		failures = 0
		pkt := make([]byte, n)
		copy(pkt, buf[:n])
		select {
		case t.queue <- pkt:
		default:
		}
	}
}

// Send implements Transport.
func (t *UDPTransport) Send(datagram []byte) error {
	select {
	case <-t.done:
		return ErrTransportClosed
	default:
	}
	_, err := t.conn.Write(datagram)
	return err
}

// Poll implements Transport.
func (t *UDPTransport) Poll() ([]byte, bool) {
	select {
	case p := <-t.queue:
		return p, true
	default:
		return nil, false
	}
}

// Err returns the error that stopped the read loop, if any.
func (t *UDPTransport) Err() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.err
}

// RemoteAddr implements Transport.
func (t *UDPTransport) RemoteAddr() *net.UDPAddr {
	return t.conn.RemoteAddr().(*net.UDPAddr)
}

// LocalAddr returns the local socket address.
func (t *UDPTransport) LocalAddr() *net.UDPAddr {
	return t.conn.LocalAddr().(*net.UDPAddr)
}

// Close implements Transport.
func (t *UDPTransport) Close() error {
	var err error
	t.once.Do(func() {
		close(t.done)
		err = t.conn.Close()
	})
	return err
}

// Pipe is an in-memory Transport pair for tests and local servers. Each end
// receives what the other sends.
type Pipe struct {
	mu     sync.Mutex
	in     [][]byte
	peer   *Pipe
	addr   *net.UDPAddr
	closed bool
}

// NewPipe returns two connected in-memory transports.
func NewPipe() (*Pipe, *Pipe) {
	a := &Pipe{addr: &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 27005}}
	b := &Pipe{addr: &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 27015}}
	a.peer, b.peer = b, a
	return a, b
}

// Send implements Transport.
func (p *Pipe) Send(datagram []byte) error {
	p.mu.Lock()
	closed := p.closed
	p.mu.Unlock()
	if closed {
		return ErrTransportClosed
	}
	cp := append([]byte(nil), datagram...)
	p.peer.mu.Lock()
	defer p.peer.mu.Unlock()
	if !p.peer.closed {
		p.peer.in = append(p.peer.in, cp)
	}
	return nil
}

// Poll implements Transport.
func (p *Pipe) Poll() ([]byte, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.in) == 0 {
		return nil, false
	}
	d := p.in[0]
	p.in = p.in[1:]
	return d, true
}

// RemoteAddr implements Transport.
func (p *Pipe) RemoteAddr() *net.UDPAddr { return p.peer.addr }

// Close implements Transport.
func (p *Pipe) Close() error {
	p.mu.Lock()
	p.closed = true
	p.mu.Unlock()
	return nil
}
