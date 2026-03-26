package proxy

import (
	"bytes"
	"crypto/tls"
	"io"
	"net"

	"github.com/infobloxopen/devedge/pkg/types"
)

// sniListener accepts raw TCP connections on port 443 and routes them
// based on the SNI hostname in the TLS ClientHello:
//   - TCP routes → serveTCP (TLS termination or passthrough + byte pipe)
//   - HTTP routes → handed to the http.Server via the returned net.Listener
//
// Connections for HTTP routes are wrapped so the peeked bytes are replayed
// for the HTTP server's TLS handshake.
func (p *Proxy) sniListener(rawLn net.Listener, tlsCfg *tls.Config) net.Listener {
	httpLn := &chanListener{
		addr: rawLn.Addr(),
		ch:   make(chan net.Conn, 64),
		done: make(chan struct{}),
	}

	go func() {
		for {
			conn, err := rawLn.Accept()
			if err != nil {
				close(httpLn.ch)
				return
			}
			go p.routeConn(conn, httpLn, tlsCfg)
		}
	}()

	return httpLn
}

// routeConn peeks at the TLS ClientHello, checks if the SNI matches a
// TCP route, and dispatches accordingly.
func (p *Proxy) routeConn(conn net.Conn, httpLn *chanListener, tlsCfg *tls.Config) {
	// Read enough bytes to parse the ClientHello (up to 16KB).
	var buf [16384]byte
	n, err := readUntilClientHello(conn, buf[:])
	if err != nil {
		conn.Close()
		return
	}

	serverName := extractSNI(buf[:n])

	// Check if this is a TCP route.
	if route, ok := p.reg.Lookup(serverName); ok && route.EffectiveProtocol() == types.ProtocolTCP {
		// Wrap conn so the peeked bytes are replayed for TLS passthrough.
		peeked := &peekedConn{Conn: conn, buf: bytes.NewReader(buf[:n])}
		p.serveTCP(peeked, serverName, route)
		return
	}

	// HTTP route — wrap and hand to the HTTP server's TLS listener.
	peeked := &peekedConn{Conn: conn, buf: bytes.NewReader(buf[:n])}
	tlsConn := tls.Server(peeked, tlsCfg)

	select {
	case httpLn.ch <- tlsConn:
	default:
		// Channel full, drop connection.
		conn.Close()
	}
}

// readUntilClientHello reads from conn into buf until we have a complete
// TLS record (or enough to extract SNI). Returns bytes read.
func readUntilClientHello(conn net.Conn, buf []byte) (int, error) {
	total := 0
	for total < len(buf) {
		n, err := conn.Read(buf[total:])
		total += n
		if total >= 5 {
			// TLS record header: type(1) + version(2) + length(2)
			recordLen := int(buf[3])<<8 | int(buf[4])
			if total >= 5+recordLen {
				return total, nil
			}
		}
		if err != nil {
			return total, err
		}
	}
	return total, nil
}

// extractSNI parses a TLS ClientHello to find the SNI extension.
// Returns "" if not found.
func extractSNI(data []byte) string {
	// Minimal TLS ClientHello parser.
	// Record layer: type(1) + version(2) + length(2)
	if len(data) < 5 || data[0] != 0x16 { // handshake
		return ""
	}
	recordLen := int(data[3])<<8 | int(data[4])
	data = data[5:]
	if len(data) < recordLen {
		return ""
	}
	data = data[:recordLen]

	// Handshake header: type(1) + length(3)
	if len(data) < 4 || data[0] != 0x01 { // client_hello
		return ""
	}
	hLen := int(data[1])<<16 | int(data[2])<<8 | int(data[3])
	data = data[4:]
	if len(data) < hLen {
		return ""
	}
	data = data[:hLen]

	// ClientHello: version(2) + random(32) = 34
	if len(data) < 34 {
		return ""
	}
	data = data[34:]

	// Session ID: length(1) + data
	if len(data) < 1 {
		return ""
	}
	sidLen := int(data[0])
	data = data[1:]
	if len(data) < sidLen {
		return ""
	}
	data = data[sidLen:]

	// Cipher suites: length(2) + data
	if len(data) < 2 {
		return ""
	}
	csLen := int(data[0])<<8 | int(data[1])
	data = data[2:]
	if len(data) < csLen {
		return ""
	}
	data = data[csLen:]

	// Compression methods: length(1) + data
	if len(data) < 1 {
		return ""
	}
	cmLen := int(data[0])
	data = data[1:]
	if len(data) < cmLen {
		return ""
	}
	data = data[cmLen:]

	// Extensions: length(2) + data
	if len(data) < 2 {
		return ""
	}
	extLen := int(data[0])<<8 | int(data[1])
	data = data[2:]
	if len(data) < extLen {
		return ""
	}
	data = data[:extLen]

	// Walk extensions looking for SNI (type 0x0000).
	for len(data) >= 4 {
		extType := int(data[0])<<8 | int(data[1])
		eLen := int(data[2])<<8 | int(data[3])
		data = data[4:]
		if len(data) < eLen {
			return ""
		}
		if extType == 0 { // server_name
			return parseSNIExtension(data[:eLen])
		}
		data = data[eLen:]
	}
	return ""
}

// parseSNIExtension extracts the hostname from an SNI extension payload.
func parseSNIExtension(data []byte) string {
	// SNI list length(2)
	if len(data) < 2 {
		return ""
	}
	data = data[2:]
	// Server name: type(1) + length(2) + name
	for len(data) >= 3 {
		nameType := data[0]
		nameLen := int(data[1])<<8 | int(data[2])
		data = data[3:]
		if len(data) < nameLen {
			return ""
		}
		if nameType == 0 { // host_name
			return string(data[:nameLen])
		}
		data = data[nameLen:]
	}
	return ""
}

// peekedConn replays buffered bytes before reading from the underlying conn.
type peekedConn struct {
	net.Conn
	buf *bytes.Reader
}

func (c *peekedConn) Read(b []byte) (int, error) {
	if c.buf != nil && c.buf.Len() > 0 {
		n, err := c.buf.Read(b)
		if err == io.EOF {
			err = nil // seamless transition to real conn
		}
		return n, err
	}
	return c.Conn.Read(b)
}

// chanListener is a net.Listener backed by a channel of connections.
type chanListener struct {
	addr net.Addr
	ch   chan net.Conn
	done chan struct{}
}

func (l *chanListener) Accept() (net.Conn, error) {
	conn, ok := <-l.ch
	if !ok {
		return nil, net.ErrClosed
	}
	return conn, nil
}

func (l *chanListener) Close() error {
	select {
	case <-l.done:
	default:
		close(l.done)
	}
	return nil
}

func (l *chanListener) Addr() net.Addr { return l.addr }
