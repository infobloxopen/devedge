package proxy

import (
	"crypto/tls"
	"io"
	"net"
	"time"

	"github.com/infobloxopen/devedge/pkg/types"
)

// serveTCP handles a TCP route by terminating TLS (or passing through)
// and piping bytes to the upstream.
func (p *Proxy) serveTCP(clientConn net.Conn, serverName string, route types.Route) {
	defer clientConn.Close()

	if route.BackendTLS {
		// TLS passthrough: we already peeked at the ClientHello but
		// haven't consumed it. The peekedConn replays the buffered
		// bytes, so the upstream sees a complete TLS handshake.
		upstream, err := net.DialTimeout("tcp", route.Upstream, 10*time.Second)
		if err != nil {
			p.logger.Error("tcp passthrough dial failed", "host", serverName, "upstream", route.Upstream, "err", err)
			return
		}
		defer upstream.Close()
		pipe(clientConn, upstream)
		return
	}

	// TLS termination: complete the handshake, then forward plaintext.
	tlsCert, err := p.getCert(p.cache, serverName)
	if err != nil {
		p.logger.Error("tcp cert generation failed", "host", serverName, "err", err)
		return
	}

	tlsConn := tls.Server(clientConn, &tls.Config{
		Certificates: []tls.Certificate{*tlsCert},
	})
	if err := tlsConn.Handshake(); err != nil {
		p.logger.Error("tcp tls handshake failed", "host", serverName, "err", err)
		return
	}
	defer tlsConn.Close()

	upstream, err := net.DialTimeout("tcp", route.Upstream, 10*time.Second)
	if err != nil {
		p.logger.Error("tcp dial failed", "host", serverName, "upstream", route.Upstream, "err", err)
		return
	}
	defer upstream.Close()

	p.logger.Info("tcp connection established", "host", serverName, "upstream", route.Upstream)
	pipe(tlsConn, upstream)
}

// pipe copies data bidirectionally between two connections.
func pipe(a, b net.Conn) {
	done := make(chan struct{}, 2)
	cp := func(dst, src net.Conn) {
		io.Copy(dst, src)
		// Signal the other direction to stop.
		if tc, ok := dst.(*net.TCPConn); ok {
			tc.CloseWrite()
		}
		done <- struct{}{}
	}
	go cp(a, b)
	go cp(b, a)
	<-done
}
