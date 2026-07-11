package ippsrv

// DNS-SD service subtype announcement.
//
// Apple clients decide whether a printer is driverless-capable by browsing
// the _universal._sub._ipp._tcp subtype; a printer that does not answer it
// is offered with a driver prompt on macOS and is invisible to iOS.  Per RFC
// 6762 §7.1 the answer must be a PTR record OWNED by the subtype name whose
// rdata is the parent-type service instance name (…._ipp._tcp.local.).  The
// brutella/dnssd responder can only answer for the names of services it
// registered, so this file implements a minimal supplementary mDNS responder
// that serves exactly that one PTR record; SRV/TXT/A resolution of the
// instance is handled by the main responder.

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"strings"
	"time"

	"github.com/miekg/dns"
	"golang.org/x/net/ipv4"
	"golang.org/x/net/ipv6"
)

const (
	mdnsPort   = 5353
	subtypeTTL = 4500 // seconds, standard TTL for shared record sets
)

var (
	mdnsGroup4 = &net.UDPAddr{IP: net.IPv4(224, 0, 0, 251), Port: mdnsPort}
	mdnsGroup6 = &net.UDPAddr{IP: net.ParseIP("ff02::fb"), Port: mdnsPort}
)

// escapeLabel escapes a DNS label for presentation format (RFC 1035), so
// that instance names containing dots or backslashes survive packing.
func escapeLabel(label string) string {
	return strings.NewReplacer(`\`, `\\`, `.`, `\.`).Replace(label)
}

// subtypeResponder answers PTR queries for one DNS-SD service subtype with
// the parent-type instance names.
type subtypeResponder struct {
	subtype   string   // fully qualified, e.g. "_universal._sub._ipp._tcp.local."
	instances []string // fully qualified parent instances, e.g. "Printer\ Name._ipp._tcp.local."

	pc4 *ipv4.PacketConn
	pc6 *ipv6.PacketConn
}

// newSubtypeResponder prepares a responder that maps the subtype to the
// given instance names of parentType (e.g. "_ipp._tcp").  IPv6 is best
// effort; IPv4 is required.
func newSubtypeResponder(subtype, parentType string, names []string) (*subtypeResponder, error) {
	r := &subtypeResponder{
		subtype: subtype + ".local.",
	}
	for _, name := range names {
		r.instances = append(r.instances, escapeLabel(name)+"."+parentType+".local.")
	}

	// Binding to the multicast group address (not the wildcard) is what
	// allows coexistence with mDNSResponder and the dnssd library on the
	// same port.
	conn4, err := net.ListenUDP("udp4", mdnsGroup4)
	if err != nil {
		return nil, fmt.Errorf("failed to listen on mDNS IPv4 group: %w", err)
	}
	r.pc4 = ipv4.NewPacketConn(conn4)
	_ = r.pc4.SetControlMessage(ipv4.FlagInterface, true)
	_ = r.pc4.SetMulticastTTL(255)
	if conn6, err := net.ListenUDP("udp6", mdnsGroup6); err == nil {
		r.pc6 = ipv6.NewPacketConn(conn6)
		_ = r.pc6.SetControlMessage(ipv6.FlagInterface, true)
		_ = r.pc6.SetMulticastHopLimit(255)
	}
	for _, iface := range multicastInterfaces() {
		if err := r.pc4.JoinGroup(&iface, &net.UDPAddr{IP: mdnsGroup4.IP}); err != nil {
			slog.Debug("mDNS IPv4 join failed", "interface", iface.Name, "error", err)
		}
		if r.pc6 != nil {
			if err := r.pc6.JoinGroup(&iface, &net.UDPAddr{IP: mdnsGroup6.IP}); err != nil {
				slog.Debug("mDNS IPv6 join failed", "interface", iface.Name, "error", err)
			}
		}
	}
	return r, nil
}

func multicastInterfaces() []net.Interface {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	var out []net.Interface
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp != 0 && iface.Flags&net.FlagMulticast != 0 {
			out = append(out, iface)
		}
	}
	return out
}

// records returns the PTR record set, with the given TTL.
func (r *subtypeResponder) records(ttl uint32) []dns.RR {
	rrs := make([]dns.RR, 0, len(r.instances))
	for _, inst := range r.instances {
		rrs = append(rrs, &dns.PTR{
			Hdr: dns.RR_Header{
				Name:   r.subtype,
				Rrtype: dns.TypePTR,
				Class:  dns.ClassINET,
				Ttl:    ttl,
			},
			Ptr: inst,
		})
	}
	return rrs
}

// respond serves subtype PTR queries until ctx is cancelled, then multicasts
// a goodbye (TTL 0) for the record set.  It announces the records on start.
func (r *subtypeResponder) respond(ctx context.Context) {
	go r.read4(ctx)
	if r.pc6 != nil {
		go r.read6(ctx)
	}

	// unsolicited announcements (RFC 6762 §8.3)
	for range 2 {
		r.multicast(r.records(subtypeTTL))
		select {
		case <-time.After(time.Second):
		case <-ctx.Done():
		}
	}

	<-ctx.Done()
	r.multicast(r.records(0)) // goodbye
	r.pc4.Close()
	if r.pc6 != nil {
		r.pc6.Close()
	}
}

func (r *subtypeResponder) read4(ctx context.Context) {
	buf := make([]byte, 65536)
	for ctx.Err() == nil {
		n, cm, src, err := r.pc4.ReadFrom(buf)
		if err != nil {
			return
		}
		var ifIndex int
		if cm != nil {
			ifIndex = cm.IfIndex
		}
		r.handle(buf[:n], src, func(msg []byte, dst net.Addr) {
			wcm := &ipv4.ControlMessage{IfIndex: ifIndex}
			_, _ = r.pc4.WriteTo(msg, wcm, dst)
		})
	}
}

func (r *subtypeResponder) read6(ctx context.Context) {
	buf := make([]byte, 65536)
	for ctx.Err() == nil {
		n, cm, src, err := r.pc6.ReadFrom(buf)
		if err != nil {
			return
		}
		var ifIndex int
		if cm != nil {
			ifIndex = cm.IfIndex
		}
		r.handle(buf[:n], src, func(msg []byte, dst net.Addr) {
			wcm := &ipv6.ControlMessage{IfIndex: ifIndex}
			_, _ = r.pc6.WriteTo(msg, wcm, dst)
		})
	}
}

// handle answers a single received packet.  write sends a packed message to
// the destination on the interface the query arrived on.
func (r *subtypeResponder) handle(pkt []byte, src net.Addr, write func(msg []byte, dst net.Addr)) {
	var q dns.Msg
	if err := q.Unpack(pkt); err != nil || q.Response {
		return
	}
	asked := false
	for _, question := range q.Question {
		if (question.Qtype == dns.TypePTR || question.Qtype == dns.TypeANY) &&
			strings.EqualFold(question.Name, r.subtype) {
			asked = true
			break
		}
	}
	if !asked {
		return
	}

	var resp dns.Msg
	resp.Response = true
	resp.Authoritative = true
	resp.Answer = r.records(subtypeTTL)

	udp, _ := src.(*net.UDPAddr)
	if udp != nil && udp.Port != mdnsPort {
		// legacy unicast query (RFC 6762 §6.7): echo ID and question,
		// respond directly to the source.
		resp.Id = q.Id
		resp.Question = q.Question
		if msg, err := resp.Pack(); err == nil {
			write(msg, src)
		}
		return
	}
	dst := net.Addr(mdnsGroup4)
	if udp != nil && udp.IP.To4() == nil {
		dst = mdnsGroup6
	}
	if msg, err := resp.Pack(); err == nil {
		write(msg, dst)
	}
}

// multicast sends the record set as an unsolicited response on all
// interfaces, over both address families.
func (r *subtypeResponder) multicast(rrs []dns.RR) {
	var resp dns.Msg
	resp.Response = true
	resp.Authoritative = true
	resp.Answer = rrs
	msg, err := resp.Pack()
	if err != nil {
		return
	}
	for _, iface := range multicastInterfaces() {
		_, _ = r.pc4.WriteTo(msg, &ipv4.ControlMessage{IfIndex: iface.Index}, mdnsGroup4)
		if r.pc6 != nil {
			_, _ = r.pc6.WriteTo(msg, &ipv6.ControlMessage{IfIndex: iface.Index}, mdnsGroup6)
		}
	}
}
