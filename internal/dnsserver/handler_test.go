package dnsserver

import (
	"io"
	"net"
	"testing"

	"github.com/infobloxopen/devedge/pkg/types"
	"github.com/miekg/dns"
)

// fakeResponseWriter captures the response written by a dns.Handler.
type fakeResponseWriter struct {
	local      net.Addr
	remote     net.Addr
	msg        *dns.Msg
	writeError error
}

func newFakeWriter(transport string) *fakeResponseWriter {
	w := &fakeResponseWriter{
		local: &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 15354},
	}
	if transport == "tcp" {
		w.local = &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 15354}
		w.remote = &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 12345}
	} else {
		w.remote = &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 12345}
	}
	return w
}

func (w *fakeResponseWriter) LocalAddr() net.Addr  { return w.local }
func (w *fakeResponseWriter) RemoteAddr() net.Addr { return w.remote }
func (w *fakeResponseWriter) WriteMsg(m *dns.Msg) error {
	if w.writeError != nil {
		return w.writeError
	}
	w.msg = m
	return nil
}
func (w *fakeResponseWriter) Write(p []byte) (int, error) { return len(p), nil }
func (w *fakeResponseWriter) Close() error                { return nil }
func (w *fakeResponseWriter) TsigStatus() error           { return nil }
func (w *fakeResponseWriter) TsigTimersOnly(bool)         {}
func (w *fakeResponseWriter) Hijack()                     {}

var _ dns.ResponseWriter = (*fakeResponseWriter)(nil)
var _ io.Writer = (*fakeResponseWriter)(nil)

func newSet(t *testing.T, suffixes ...string) *AuthoritativeSet {
	t.Helper()
	s := NewAuthoritativeSet()
	in := make([]ConfiguredSuffix, 0, len(suffixes))
	for _, n := range suffixes {
		in = append(in, mustSuffix(t, n))
	}
	s.Replace(in)
	return s
}

func newReq(name string, qtype uint16) *dns.Msg {
	m := new(dns.Msg)
	m.SetQuestion(dns.Fqdn(name), qtype)
	return m
}

func TestHandler_AInSuffix_ReturnsEdgeIP(t *testing.T) {
	set := newSet(t, "dev.test")
	h := NewHandler(set, net.ParseIP(types.EdgeIP), nil)
	w := newFakeWriter("udp")

	h.ServeDNS(w, newReq("any.dev.test", dns.TypeA))

	if w.msg == nil {
		t.Fatal("handler did not write a response")
	}
	if w.msg.Rcode != dns.RcodeSuccess {
		t.Errorf("rcode = %d, want NOERROR", w.msg.Rcode)
	}
	if len(w.msg.Answer) != 1 {
		t.Fatalf("answer count = %d, want 1", len(w.msg.Answer))
	}
	a, ok := w.msg.Answer[0].(*dns.A)
	if !ok {
		t.Fatalf("answer is %T, want *dns.A", w.msg.Answer[0])
	}
	if !a.A.Equal(net.ParseIP(types.EdgeIP)) {
		t.Errorf("answer A = %s, want %s", a.A, types.EdgeIP)
	}
	if a.Hdr.Ttl != answerTTL {
		t.Errorf("TTL = %d, want %d", a.Hdr.Ttl, answerTTL)
	}
}

func TestHandler_AAAAInSuffix_EmptyAnswer(t *testing.T) {
	set := newSet(t, "dev.test")
	h := NewHandler(set, net.ParseIP(types.EdgeIP), nil)
	w := newFakeWriter("udp")

	h.ServeDNS(w, newReq("any.dev.test", dns.TypeAAAA))

	if w.msg == nil {
		t.Fatal("handler did not write a response")
	}
	if w.msg.Rcode != dns.RcodeSuccess {
		t.Errorf("rcode = %d, want NOERROR", w.msg.Rcode)
	}
	if len(w.msg.Answer) != 0 {
		t.Errorf("answer count = %d, want 0", len(w.msg.Answer))
	}
	if len(w.msg.Ns) != 0 {
		t.Errorf("authority section non-empty: %v", w.msg.Ns)
	}
}

func TestHandler_OtherTypeInSuffix_EmptyAnswer(t *testing.T) {
	set := newSet(t, "dev.test")
	h := NewHandler(set, net.ParseIP(types.EdgeIP), nil)

	for _, qt := range []uint16{dns.TypeMX, dns.TypeTXT, dns.TypeSRV, dns.TypeCNAME} {
		w := newFakeWriter("udp")
		h.ServeDNS(w, newReq("any.dev.test", qt))
		if w.msg == nil {
			t.Fatalf("qtype %s: no response", dns.TypeToString[qt])
		}
		if w.msg.Rcode != dns.RcodeSuccess {
			t.Errorf("qtype %s: rcode = %d, want NOERROR", dns.TypeToString[qt], w.msg.Rcode)
		}
		if len(w.msg.Answer) != 0 {
			t.Errorf("qtype %s: answer = %v, want empty", dns.TypeToString[qt], w.msg.Answer)
		}
	}
}

func TestHandler_OutOfSuffix_Refused(t *testing.T) {
	set := newSet(t, "dev.test")
	h := NewHandler(set, net.ParseIP(types.EdgeIP), nil)
	w := newFakeWriter("udp")

	h.ServeDNS(w, newReq("example.com", dns.TypeA))

	if w.msg.Rcode != dns.RcodeRefused {
		t.Errorf("rcode = %d, want REFUSED", w.msg.Rcode)
	}
	if len(w.msg.Answer) != 0 {
		t.Errorf("refused response has answers: %v", w.msg.Answer)
	}
}

func TestHandler_EmptyAuthoritativeSet_AllRefused(t *testing.T) {
	set := NewAuthoritativeSet()
	h := NewHandler(set, net.ParseIP(types.EdgeIP), nil)
	for _, name := range []string{"dev.test", "foo.example.com", "x.y.z.test"} {
		w := newFakeWriter("udp")
		h.ServeDNS(w, newReq(name, dns.TypeA))
		if w.msg.Rcode != dns.RcodeRefused {
			t.Errorf("name %s rcode = %d, want REFUSED", name, w.msg.Rcode)
		}
	}
}

func TestHandler_TrailingDotAndCase(t *testing.T) {
	set := newSet(t, "dev.test")
	h := NewHandler(set, net.ParseIP(types.EdgeIP), nil)

	for _, name := range []string{"Any.DEV.TEST", "any.dev.test.", "ANY.DEV.TEST."} {
		w := newFakeWriter("udp")
		h.ServeDNS(w, newReq(name, dns.TypeA))
		if w.msg.Rcode != dns.RcodeSuccess {
			t.Errorf("name %s rcode = %d, want NOERROR", name, w.msg.Rcode)
		}
		if len(w.msg.Answer) != 1 {
			t.Errorf("name %s answer count = %d, want 1", name, len(w.msg.Answer))
		}
	}
}

func TestHandler_MalformedQuery_FormErr(t *testing.T) {
	set := newSet(t, "dev.test")
	h := NewHandler(set, net.ParseIP(types.EdgeIP), nil)

	// Zero questions.
	w := newFakeWriter("udp")
	zero := new(dns.Msg)
	zero.Id = dns.Id()
	h.ServeDNS(w, zero)
	if w.msg.Rcode != dns.RcodeFormatError {
		t.Errorf("zero-question rcode = %d, want FORMERR", w.msg.Rcode)
	}

	// Multi question.
	w = newFakeWriter("udp")
	multi := new(dns.Msg)
	multi.SetQuestion("a.dev.test.", dns.TypeA)
	multi.Question = append(multi.Question, dns.Question{Name: "b.dev.test.", Qtype: dns.TypeA, Qclass: dns.ClassINET})
	h.ServeDNS(w, multi)
	if w.msg.Rcode != dns.RcodeFormatError {
		t.Errorf("multi-question rcode = %d, want FORMERR", w.msg.Rcode)
	}
}

// US3: wildcard semantics. Any name inside a configured suffix resolves,
// regardless of whether it was ever registered as a route. These tests
// lock the wildcard behavior in place — a future change to "answer only
// for registered names" would fail CI.

func TestHandler_NeverRegisteredName_ResolvesToEdgeIP(t *testing.T) {
	set := newSet(t, "dev.test")
	h := NewHandler(set, net.ParseIP(types.EdgeIP), nil)
	w := newFakeWriter("udp")

	// A randomized name that no one would ever register; still resolves.
	h.ServeDNS(w, newReq("brand-new-never-registered.dev.test", dns.TypeA))

	if w.msg.Rcode != dns.RcodeSuccess || len(w.msg.Answer) != 1 {
		t.Fatalf("unregistered name: rcode=%d answers=%v", w.msg.Rcode, w.msg.Answer)
	}
	a := w.msg.Answer[0].(*dns.A)
	if !a.A.Equal(net.ParseIP(types.EdgeIP)) {
		t.Errorf("answer = %s, want %s", a.A, types.EdgeIP)
	}
}

func TestHandler_DeepSubdomain_Resolves(t *testing.T) {
	set := newSet(t, "dev.test")
	h := NewHandler(set, net.ParseIP(types.EdgeIP), nil)
	w := newFakeWriter("udp")

	h.ServeDNS(w, newReq("a.b.c.d.e.dev.test", dns.TypeA))

	if w.msg.Rcode != dns.RcodeSuccess || len(w.msg.Answer) != 1 {
		t.Fatalf("deep subdomain: rcode=%d answers=%v", w.msg.Rcode, w.msg.Answer)
	}
}

// TestHandler_HandlerDoesNotConsultRegistry proves the wildcard property
// by construction: the handler depends only on the AuthoritativeSet and
// EdgeIP — never the route registry. A configured suffix with an empty
// registry must still resolve in-suffix names.
func TestHandler_HandlerDoesNotConsultRegistry(t *testing.T) {
	set := newSet(t, "dev.test")
	h := NewHandler(set, net.ParseIP(types.EdgeIP), nil)
	w := newFakeWriter("udp")

	h.ServeDNS(w, newReq("anything.dev.test", dns.TypeA))

	if w.msg.Rcode != dns.RcodeSuccess || len(w.msg.Answer) != 1 {
		t.Fatalf("wildcard with empty registry context: rcode=%d answers=%v", w.msg.Rcode, w.msg.Answer)
	}
}

func TestHandler_TCPTransportTagged(t *testing.T) {
	// Smoke check that a TCP RemoteAddr does not break the handler.
	set := newSet(t, "dev.test")
	h := NewHandler(set, net.ParseIP(types.EdgeIP), nil)
	w := newFakeWriter("tcp")

	h.ServeDNS(w, newReq("foo.dev.test", dns.TypeA))
	if w.msg == nil || w.msg.Rcode != dns.RcodeSuccess {
		t.Fatalf("tcp response: %v", w.msg)
	}
}
