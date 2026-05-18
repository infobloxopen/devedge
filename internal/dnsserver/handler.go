package dnsserver

import (
	"log/slog"
	"net"

	"github.com/miekg/dns"
)

// answerTTL is the fixed TTL for synthetic A records (research.md R3).
const answerTTL = 60

// handler implements dns.Handler. It consults the AuthoritativeSet and
// answers per contracts/dns-protocol.md §"Query handling rules".
type handler struct {
	set     *AuthoritativeSet
	edgeIP  net.IP
	logger  *slog.Logger
}

// NewHandler returns a dns.Handler that synthesizes wildcard answers for
// names inside the AuthoritativeSet and REFUSES everything else.
//
// edgeIP must be an IPv4 address (the loopback alias the local edge
// proxy is bound to). nil edgeIP causes a panic — callers must pass
// pkg/types.EdgeIP.
func NewHandler(set *AuthoritativeSet, edgeIP net.IP, logger *slog.Logger) dns.Handler {
	if set == nil {
		panic("dnsserver.NewHandler: nil AuthoritativeSet")
	}
	if edgeIP == nil {
		panic("dnsserver.NewHandler: nil edgeIP")
	}
	v4 := edgeIP.To4()
	if v4 == nil {
		panic("dnsserver.NewHandler: edgeIP is not an IPv4 address")
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &handler{set: set, edgeIP: v4, logger: logger}
}

func (h *handler) ServeDNS(w dns.ResponseWriter, req *dns.Msg) {
	resp := new(dns.Msg)

	// RFC 1035 forbids zero or multi-question queries.
	if req == nil || len(req.Question) != 1 {
		if req != nil {
			resp.SetRcode(req, dns.RcodeFormatError)
		} else {
			resp.MsgHdr.Rcode = dns.RcodeFormatError
		}
		_ = w.WriteMsg(resp)
		return
	}

	q := req.Question[0]
	transport := "udp"
	if _, ok := w.RemoteAddr().(*net.TCPAddr); ok {
		transport = "tcp"
	}

	if _, inSuffix := h.set.Match(q.Name); !inSuffix {
		resp.SetRcode(req, dns.RcodeRefused)
		_ = w.WriteMsg(resp)
		h.logger.Debug("dnsserver.query",
			"name", q.Name,
			"qtype", dns.TypeToString[q.Qtype],
			"transport", transport,
			"in_suffix", false,
			"rcode", "REFUSED",
		)
		return
	}

	resp.SetReply(req)
	resp.Authoritative = true

	switch q.Qtype {
	case dns.TypeA:
		rr := &dns.A{
			Hdr: dns.RR_Header{
				Name:   q.Name,
				Rrtype: dns.TypeA,
				Class:  dns.ClassINET,
				Ttl:    answerTTL,
			},
			A: h.edgeIP,
		}
		resp.Answer = []dns.RR{rr}
	default:
		// AAAA, MX, TXT, SRV, anything else: NOERROR + empty answer.
	}

	if err := w.WriteMsg(resp); err != nil {
		h.logger.Info("dnsserver.query_write_failed",
			"name", q.Name,
			"qtype", dns.TypeToString[q.Qtype],
			"transport", transport,
			"err", err,
		)
		return
	}

	h.logger.Debug("dnsserver.query",
		"name", q.Name,
		"qtype", dns.TypeToString[q.Qtype],
		"transport", transport,
		"in_suffix", true,
		"rcode", "NOERROR",
	)
}
