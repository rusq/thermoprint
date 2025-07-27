// Package ippsrv implements an IPP server.
package ippsrv

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/OpenPrinting/goipp"
	"github.com/rusq/httpex"
)

var MaxDocumentSize int64 = 104857600

type Server struct {
	pp  []Printer       // List of printers managed by the server
	srv *http.Server    // HTTP server instance
	is  *basicIPPServer // IPP server instance

	debug   bool
	dumpdir string
}

// https://datatracker.ietf.org/doc/html/rfc8011
const (
	hdrURIAuthenticationSupported = "uri-authentication-supported"
	hdrURISecuritySupported       = "uri-security-supported"
	hdrPrinterURISupported        = "printer-uri-supported"

	hdrContentType = "Content-Type"
	ippMIMEType    = "application/ipp"
)

// Option is the server option.
type Option func(*Server)

func WithDebug(b bool) Option {
	return func(s *Server) {
		s.debug = b
	}
}

// WithDumpDir allows to set the directory for protocol dumps.
// If not specified, a temporary directory will be used.
func WithDumpDir(dir string) Option {
	return func(s *Server) {
		s.dumpdir = dir
	}
}

func WithAdditionalPrinters(pp ...Printer) Option {
	return func(s *Server) {
		s.pp = append(s.pp, pp...)
	}
}

// New returns a new IPP server.
func New(p Printer, opts ...Option) (*Server, error) {
	var s = &Server{
		pp: []Printer{p},
	}
	for _, opt := range opts {
		opt(s)
	}
	if s.debug {
		if s.dumpdir != "" {
			if err := os.MkdirAll(s.dumpdir, 0700); err != nil {
				return nil, fmt.Errorf("error creating requested dump directory: %w", err)
			}
		} else {
			d, err := os.MkdirTemp("", "protodump-*")
			if err != nil {
				return nil, fmt.Errorf("error creating temporary dump directory: %w", err)
			}
			s.dumpdir = d
		}
		slog.Info("protocol dump", "directory", s.dumpdir)
	}

	ippsrv, err := newBasicIPPServer("/printers/", s.pp...)
	if err != nil {
		return nil, err
	}
	s.is = ippsrv

	m := http.NewServeMux()
	m.HandleFunc("/admin/", s.handleAdmin)
	m.HandleFunc("POST /printers/{name}", s.handlePrint)
	m.HandleFunc("POST /printers/{name}/{job}", s.handleJob)
	m.HandleFunc("/", s.handlePrint)
	srv := &http.Server{
		Handler: httpex.LogMiddleware(m, log.Default()),
	}
	s.srv = srv

	return s, nil
}

// Info is the SIGINFO response for the server.
func (s *Server) Info(w io.Writer) {
	fmt.Fprintf(w, "*** IPP Server Info ***\n")
	fmt.Fprintf(w, "Base URL: %s\n", s.is.baseURL)
	fmt.Fprintf(w, "Printers:\n")
	for name := range s.is.Printer {
		fmt.Fprintf(w, "  - %s\n", name)
	}
	fmt.Fprintf(w, "Server Address: %s\n", s.srv.Addr)
	fmt.Fprintf(w, "Debug Mode: %t\n", s.debug)
	fmt.Fprintf(w, "Max Document Size: %d bytes\n", MaxDocumentSize)

	fmt.Fprint(w, "Spool status:\n")
	if jobs, err := s.is.spool.ListJobs(); err != nil {
		if errors.Is(err, errJobNotFound) {
			fmt.Fprint(w, "  No jobs found\n")
		} else {
			return
		}
	} else {
		for _, job := range jobs {
			fmt.Fprintf(w, "  - Job ID: %d, Printer: %s, Status: %s\n", job.ID,
				job.Printer.Name(), job.State)
		}
	}
}

func (s *Server) handleJob(w http.ResponseWriter, r *http.Request) {
	prnName := r.PathValue("name")
	if prnName == "" {
		http.NotFound(w, r)
		return
	}
	jobName := r.PathValue("job")
	if jobName == "" {
		slog.ErrorContext(r.Context(), "job name is empty", "endpoint", "jobs", "method", r.Method)
		http.NotFound(w, r)
		return
	}
	jobID, err := strconv.Atoi(jobName)
	if err != nil {
		slog.ErrorContext(r.Context(), "invalid job ID", "error", err, "job", jobName)
		http.NotFound(w, r)
		return
	}
	if jobID < 1 {
		slog.ErrorContext(r.Context(), "job ID must be greater than 0", "job", jobName)
		httpError(w, http.StatusBadRequest)
		return
	}
	slog.InfoContext(r.Context(), "jobs requested", "endpoint", "jobs", "method", r.Method)
}

func (s *Server) handleAdmin(w http.ResponseWriter, r *http.Request) {
	slog.InfoContext(r.Context(), "admin requested", "endpoint", "admin", "method", r.Method)
}

func httpError(w http.ResponseWriter, code int) {
	http.Error(w, fmt.Sprintf("%d %s", code, http.StatusText(code)), code)
}

func (s *Server) handlePrint(w http.ResponseWriter, r *http.Request) {
	if r.Body != nil {
		defer r.Body.Close()
	} else {
		httpError(w, http.StatusBadRequest)
		return
	}

	name := r.PathValue("name")
	if name == "" {
		http.NotFound(w, r)
		return
	}
	slog.Info("print request", "method", r.Method, "printer", name)

	// parse the IPP message
	var msg goipp.Message
	if err := msg.Decode(r.Body); err != nil {
		http.Error(w, "bad request", 400)
		return
	}
	payload, err := io.ReadAll(io.LimitReader(r.Body, MaxDocumentSize))
	if err != nil {
		slog.Warn("failed to read the payload", "error", err)
	} else {
		slog.Info("payload length", "length", len(payload))
	}
	if s.debug {
		t := time.Now()
		dumpIPPFile(
			filepath.Join(s.dumpdir, fmt.Sprintf("print_request_%d_%04x.ipp", t.Unix(), msg.Code)),
			&msg,
		)
		dumpfile(
			filepath.Join(s.dumpdir, fmt.Sprintf("print_request_%d_%04x.json", t.Unix(), msg.Code)),
			&msg,
		)
	}
	// Pass the control to the IPP server handler
	w.Header().Set(hdrContentType, ippMIMEType)
	resp, err := s.is.ServeIPP(r.Context(), &msg, payload)
	if err != nil {
		baseResponse(scServerError).Encode(w)
		slog.Error("failed to handle print request", "error", err)
		return
	}
	if err := resp.Encode(w); err != nil {
		slog.Error("failed to encode response", "error", err)
		httpError(w, http.StatusInternalServerError)
		return
	}
}

func (s *Server) ListenAndServe(addr string) error {
	s.srv.Addr = addr
	return s.srv.ListenAndServe()
}

func (s *Server) Shutdown(ctx context.Context) error {
	if s.srv == nil {
		return nil // nothing to shutdown
	}
	sctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	var errs error
	for _, fn := range []func(ctx context.Context) error{
		s.is.Shutdown,
		s.srv.Shutdown,
	} {
		if err := fn(sctx); err != nil {
			errs = errors.Join(errs, err)
		}
	}
	return errs
}
