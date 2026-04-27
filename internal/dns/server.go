// Package dns implements a tiny authoritative responder for *.test names.
// A/AAAA queries answer with loopback; everything else returns NXDOMAIN.
// Listens on UDP and TCP simultaneously.
package dns

import (
	"context"
	"net"
	"strings"
	"sync"

	mdns "github.com/miekg/dns"
)

type Server struct {
	addr    string
	target4 net.IP
	target6 net.IP
}

func New(addr string) *Server {
	return &Server{
		addr:    addr,
		target4: net.IPv4(127, 0, 0, 1),
		target6: net.ParseIP("::1"),
	}
}

func (s *Server) Run(ctx context.Context) error {
	handler := mdns.HandlerFunc(s.handle)
	udp := &mdns.Server{Addr: s.addr, Net: "udp", Handler: handler}
	tcp := &mdns.Server{Addr: s.addr, Net: "tcp", Handler: handler}

	var wg sync.WaitGroup
	errs := make(chan error, 2)
	wg.Add(2)
	go func() {
		defer wg.Done()
		if err := udp.ListenAndServe(); err != nil {
			errs <- err
		}
	}()
	go func() {
		defer wg.Done()
		if err := tcp.ListenAndServe(); err != nil {
			errs <- err
		}
	}()

	select {
	case <-ctx.Done():
	case err := <-errs:
		_ = udp.Shutdown()
		_ = tcp.Shutdown()
		wg.Wait()
		return err
	}
	_ = udp.Shutdown()
	_ = tcp.Shutdown()
	wg.Wait()
	return nil
}

func (s *Server) handle(w mdns.ResponseWriter, req *mdns.Msg) {
	msg := new(mdns.Msg)
	msg.SetReply(req)
	msg.Authoritative = true

	if len(req.Question) == 0 {
		msg.Rcode = mdns.RcodeFormatError
		_ = w.WriteMsg(msg)
		return
	}
	q := req.Question[0]
	name := strings.ToLower(strings.TrimSuffix(q.Name, "."))

	if !isTestName(name) {
		msg.Rcode = mdns.RcodeNameError
		_ = w.WriteMsg(msg)
		return
	}

	switch q.Qtype {
	case mdns.TypeA:
		msg.Answer = append(msg.Answer, &mdns.A{
			Hdr: mdns.RR_Header{Name: q.Name, Rrtype: mdns.TypeA, Class: mdns.ClassINET, Ttl: 60},
			A:   s.target4,
		})
	case mdns.TypeAAAA:
		msg.Answer = append(msg.Answer, &mdns.AAAA{
			Hdr:  mdns.RR_Header{Name: q.Name, Rrtype: mdns.TypeAAAA, Class: mdns.ClassINET, Ttl: 60},
			AAAA: s.target6,
		})
	}
	_ = w.WriteMsg(msg)
}

func isTestName(name string) bool {
	return name == "test" || strings.HasSuffix(name, ".test")
}
