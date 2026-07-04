package ippsrv

// Bonjour/DNS-SD advertisement of the IPP service (RFC 6762/6763), so that
// the printer appears in e.g. macOS "Printers & Scanners" -> Add Printer.
//
// References:
//   - https://developer.apple.com/bonjour/printing-specification/

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path"
	"strings"

	"github.com/brutella/dnssd"
)

// WithBonjour enables Bonjour/DNS-SD advertisement of the printers when the
// server starts listening.
func WithBonjour() Option {
	return func(s *Server) {
		s.bonjour.enabled = true
	}
}

const svcTypeIPP = "_ipp._tcp"

// txtRecord returns the DNS-SD TXT record for the printer, according to the
// Bonjour Printing Specification.  hostname is the mDNS host label without
// the ".local" suffix.
func txtRecord(p PrinterInformer, baseURL, hostname string, port int) map[string]string {
	return map[string]string{
		"txtvers":  "1",
		"qtotal":   "1",
		"rp":       strings.TrimPrefix(path.Join(baseURL, p.Name()), "/"),
		"ty":       p.MakeAndModel(),
		"note":     p.Info(),
		"pdl":      ippApplicationPDF.String(),
		"adminurl": fmt.Sprintf("http://%s.local.:%d/admin/", hostname, port),
		"UUID":     p.UUID(),
		"Color":    "F",
		"Duplex":   "F",
		"priority": "0",
	}
}

// localHostname returns the local host name with the ".local" suffix
// stripped, suitable for use as an mDNS host label.
func localHostname() (string, error) {
	h, err := os.Hostname()
	if err != nil {
		return "", err
	}
	h = strings.TrimSuffix(h, ".")
	h = strings.TrimSuffix(h, ".local")
	if h == "" {
		return "", errors.New("empty hostname")
	}
	return h, nil
}

// checkAdvertisable returns an error if the listener address is not usable
// for DNS-SD advertisement.
func checkAdvertisable(ta *net.TCPAddr) error {
	if ta.IP.IsLoopback() {
		return fmt.Errorf("listening on loopback address %s: the printer would be advertised as <hostname>.local:%d, which is unreachable; use -addr :%d", ta, ta.Port, ta.Port)
	}
	return nil
}

// instanceName returns the DNS-SD service instance name for the printer.  It
// only includes the printer name when the server hosts more than one printer,
// to keep instance names unique.
func instanceName(p PrinterInformer, numPrinters int) string {
	if numPrinters > 1 {
		return fmt.Sprintf("%s (%s)", p.MakeAndModel(), p.Name())
	}
	return p.MakeAndModel()
}

// startBonjour starts advertising all printers on the mDNS network.  ta must
// be the address the HTTP server is bound to.  It returns once the responder
// is running; the advertisement is withdrawn by stopBonjour.
func (s *Server) startBonjour(ta *net.TCPAddr) error {
	if err := checkAdvertisable(ta); err != nil {
		return err
	}
	host, err := localHostname()
	if err != nil {
		return fmt.Errorf("cannot determine hostname: %w", err)
	}
	rsp, err := dnssd.NewResponder()
	if err != nil {
		return fmt.Errorf("failed to create DNS-SD responder: %w", err)
	}
	for _, p := range s.pp {
		sv, err := dnssd.NewService(dnssd.Config{
			Name:   instanceName(p, len(s.pp)),
			Type:   svcTypeIPP,
			Domain: "local",
			Host:   host,
			Port:   ta.Port,
			Text:   txtRecord(p, s.is.baseURL, host, ta.Port),
		})
		if err != nil {
			return fmt.Errorf("failed to create DNS-SD service for printer %q: %w", p.Name(), err)
		}
		if _, err := rsp.Add(sv); err != nil {
			return fmt.Errorf("failed to register DNS-SD service for printer %q: %w", p.Name(), err)
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	s.bonjour.cancel = cancel
	s.bonjour.done = make(chan struct{})
	go func() {
		defer close(s.bonjour.done)
		// Respond blocks; on ctx cancellation it sends goodbye packets and
		// returns.
		if err := rsp.Respond(ctx); err != nil && !errors.Is(err, context.Canceled) {
			slog.Error("DNS-SD responder stopped", "error", err)
		}
	}()
	slog.Info("bonjour advertisement started", "type", svcTypeIPP, "host", host+".local.", "port", ta.Port)
	return nil
}

// stopBonjour withdraws the advertisement, waiting for the goodbye packets to
// be sent, or ctx to expire.  It is a no-op if the advertisement never
// started.
func (s *Server) stopBonjour(ctx context.Context) {
	if s.bonjour.cancel == nil {
		return
	}
	s.bonjour.cancel()
	select {
	case <-s.bonjour.done:
	case <-ctx.Done():
	}
}
