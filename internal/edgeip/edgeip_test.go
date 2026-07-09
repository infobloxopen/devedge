package edgeip

import "testing"

func TestRoutable(t *testing.T) {
	// 127.0.0.1 is always bound to loopback.
	if !Routable("127.0.0.1") {
		t.Fatal("127.0.0.1 should be routable")
	}
	// 192.0.2.0/24 is TEST-NET-1 (RFC 5737): guaranteed not assigned to this
	// host, so binding it fails with "can't assign requested address".
	if Routable("192.0.2.1") {
		t.Fatal("192.0.2.1 (TEST-NET-1) should not be routable on this host")
	}
}
