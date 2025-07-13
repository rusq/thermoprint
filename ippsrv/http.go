// Package ippsrv implements an IPP server.
package ippsrv

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/OpenPrinting/goipp"
	"github.com/rusq/osenv/v2"
)

var MaxDocumentSize int64 = 104857600

var Debug = osenv.Value("DEBUG", true)

type Server struct {
	pp  []Printer       // List of printers managed by the server
	srv *http.Server    // HTTP server instance
	is  *basicIPPServer // IPP server instance
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

// New returns a new IPP server.
func New(pp ...Printer) (*Server, error) {
	if len(pp) == 0 {
		return nil, errors.New("at least one printer must be provided")
	}
	ippsrv, err := newBasicIPPServer("/printers/", pp...)
	if err != nil {
		return nil, err
	}
	var s = &Server{
		pp: pp,
		is: ippsrv,
	}

	m := http.NewServeMux()
	m.HandleFunc("/admin/", s.handleAdmin)
	m.HandleFunc("POST /printers/{name}", s.handlePrint)
	m.HandleFunc("POST /printers/{name}/{job}", s.handleJob)
	m.HandleFunc("/", s.handlePrint)
	srv := &http.Server{
		Handler: m,
	}
	s.srv = srv

	return s, nil
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
	{
		t := time.Now()
		dumpIPPFile(fmt.Sprintf("protodump/print_request_%d_%04x.ipp", t.Unix(), msg.Code), &msg)
		dumpfile(fmt.Sprintf("protodump/print_request_%d_%04x.json", t.Unix(), msg.Code), &msg)
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
