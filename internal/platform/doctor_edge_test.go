package platform

import (
	"net"
	"strings"
	"testing"
)

func TestCheckEdgeIP_Routable_Passes(t *testing.T) {
	// 127.0.0.1 is always on loopback.
	r := checkEdgeIPFor("127.0.0.1")
	if !r.Passed {
		t.Fatalf("expected pass for 127.0.0.1, got: %s", r.Message)
	}
}

func TestCheckEdgeIP_AliasMissing_Fails(t *testing.T) {
	// TEST-NET-1 (RFC 5737) is never assigned to this host, so binding it fails
	// with "can't assign requested address" — the exact loopback-alias-missing
	// signature.
	r := checkEdgeIPFor("192.0.2.1")
	if r.Passed {
		t.Fatalf("expected fail for unaliased 192.0.2.1, got pass: %s", r.Message)
	}
	if !strings.Contains(r.Message, "not routable") {
		t.Fatalf("message should explain the alias is missing, got: %s", r.Message)
	}
}

func TestCheckEdgeListening_Up_Passes(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	r := checkEdgeListeningFor(ln.Addr().String())
	if !r.Passed {
		t.Fatalf("expected pass against a live listener, got: %s", r.Message)
	}
}

func TestCheckEdgeListening_Down_Fails(t *testing.T) {
	// Bind then close to obtain a port that is now free — dialing it is refused.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().String()
	ln.Close()

	r := checkEdgeListeningFor(addr)
	if r.Passed {
		t.Fatalf("expected fail against a closed port, got pass: %s", r.Message)
	}
	if !strings.Contains(r.Message, "nothing listening") {
		t.Fatalf("message should say nothing is listening, got: %s", r.Message)
	}
}
