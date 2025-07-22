// Package ippsrv implements a basic IPP server that handles print jobs and printer attributes.
//
// References:
//  - https://datatracker.ietf.org/doc/html/rfc8011
//  - https://datatracker.ietf.org/doc/html/rfc3510

package ippsrv

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"time"

	"github.com/OpenPrinting/goipp"
)

type basicIPPServer struct {
	baseURL string
	Printer map[string]Printer
	spool   spooler // Spooler for managing print jobs
}

type IPPHandler interface {
	ServeIPP(ctx context.Context, req *goipp.Message, body []byte) (resp *goipp.Message, err error)
}

type IPPRequest struct {
	msg *goipp.Message
	p   Printer
}

type IPPHandlerFunc func(ctx context.Context, req *goipp.Message, body []byte) (resp *goipp.Message, err error)

func (f IPPHandlerFunc) ServeIPP(ctx context.Context, req *goipp.Message, body []byte) (resp *goipp.Message, err error) {
	return f(ctx, req, body)
}

func newBasicIPPServer(baseURL string, pp ...Printer) (*basicIPPServer, error) {
	if len(pp) == 0 {
		return nil, fmt.Errorf("at least one printer must be provided")
	}
	spool, err := newSpool("spool")
	if err != nil {
		return nil, err
	}
	var printers = make(map[string]Printer, len(pp))
	for _, p := range pp {
		if p == nil {
			return nil, fmt.Errorf("printer cannot be nil")
		}
		if p.Name() == "" {
			return nil, fmt.Errorf("printer IPP name cannot be empty")
		}
		if _, exists := printers[p.Name()]; exists {
			return nil, fmt.Errorf("printer with IPP name %q already exists", p.Name())
		}
		p.SetState(PSIdle) // Set initial state to idle
		printers[p.Name()] = p
	}

	return &basicIPPServer{
		baseURL: baseURL,
		Printer: printers, //TODO
		spool:   spool,
	}, nil
}

func (ih *basicIPPServer) Shutdown(ctx context.Context) error {
	slog.Info("shutting down IPP server")
	if ih.spool != nil {
		if err := ih.spool.Close(); err != nil {
			return nil
		}
	}
	slog.Info("IPP server shut down successfully")
	return nil
}

func (ih *basicIPPServer) ServeIPP(ctx context.Context, req *goipp.Message, body []byte) (resp *goipp.Message, err error) {
	lg := slog.With("code", req.Code, "request_id", req.RequestID)
	lg.Info("ipp request received")
	var handlers = map[goipp.Op]IPPHandlerFunc{
		goipp.OpGetPrinterAttributes: ih.handleGetPrinterAttributes,
		goipp.OpCupsGetPrinters:      ih.handleGetPrinterAttributes,
		goipp.OpCupsGetDefault:       ih.handleGetPrinterAttributes,
		goipp.OpValidateJob:          ih.handleWithBaseResponse,
		goipp.OpGetJobs:              ih.handleWithBaseResponse,
		goipp.OpGetJobAttributes:     ih.handleGetJobAttributes,
		goipp.OpPrintJob:             ih.handlePrintJob,
	}
	next, ok := handlers[goipp.Op(req.Code)]
	if !ok || next == nil {
		lg.Error("unsupported operation", "code", req.Code, "is_mapped", ok)
		return nil, fmt.Errorf("unsupported operation: %d", req.Code)
	}
	slog.Debug("ipp request", "code", req.Code, "request_id", req.RequestID)
	return next(ctx, req, body)
}

func (ih *basicIPPServer) printerAttributes(p Printer) *goipp.Message {
	m := baseResponse(scSuccessful)
	a := adder(m.Operation)
	a("printer-uri-supported", goipp.TagURI, goipp.String(ih.baseURL))
	a("uri-authentication-supported", goipp.TagKeyword, ippNone)
	a("uri-security-supported", goipp.TagKeyword, ippNone)
	a("printer-name", goipp.TagName, goipp.String(p.Name()))
	a("printer-info", goipp.TagText, goipp.String(p.Info()))
	a("printer-make-and-model", goipp.TagText, goipp.String(p.MakeAndModel()))
	a("printer-state", goipp.TagEnum, goipp.Integer(p.State()))
	a("printer-state-reasons", goipp.TagKeyword, ippNone)
	a("ipp-versions-supported", goipp.TagKeyword, goipp.String("1.1"))
	a("operations-supported", goipp.TagEnum,
		goipp.Integer(goipp.OpPrintJob),
		goipp.Integer(goipp.OpValidateJob),
		goipp.Integer(goipp.OpCancelJob),
		goipp.Integer(goipp.OpGetJobs),
		goipp.Integer(goipp.OpGetJobAttributes),
		goipp.Integer(goipp.OpGetPrinterAttributes),
	)
	a("multiple-document-jobs-supported", goipp.TagBoolean, goipp.Boolean(false))
	a("charset-configured", goipp.TagCharset, ippUTF8)
	a("charset-supported", goipp.TagCharset, ippUTF8)
	a("natural-language-configured", goipp.TagLanguage, ippENUS)
	a("generated-natural-language-supported", goipp.TagLanguage, ippENUS)
	a("document-format-default", goipp.TagMimeType, ippApplicationPDF)
	a("document-format-supported", goipp.TagMimeType, ippApplicationPDF)
	a("printer-is-accepting-jobs", goipp.TagBoolean, goipp.Boolean(p.Ready()))
	a("queued-job-count", goipp.TagInteger, goipp.Integer(ih.spool.GetJobCount(p.Name()))) // TODO: interrogate spooler for queued jobs for this printer
	a("pdl-override-supported", goipp.TagKeyword, goipp.String("not-attempted"))
	a("printer-up-time", goipp.TagInteger, goipp.Integer(p.UpTime()))
	a("compression-supported", goipp.TagKeyword, ippNone)
	a("media-supported", goipp.TagKeyword, stringsToValues(p.MediaSupported())...)
	a("media-default", goipp.TagKeyword, goipp.String(p.MediaDefault()))
	a("printer-uuid", goipp.TagURI, goipp.String(p.UUID()))

	return m
}

func (ih *basicIPPServer) handleGetPrinterAttributes(ctx context.Context, req *goipp.Message, _ []byte) (resp *goipp.Message, err error) {
	p, err := ih.printerFromRequest(req)
	if err != nil {
		return nil, fmt.Errorf("failed to get printer: %w", err)
	}
	lg := slog.With("printer", p.Name(), "code", req.Code, "request_id", req.RequestID)
	attrs, ok := findAttr(req.Operation, "requested-attributes")
	lg.Debug("requested attributes", "ok", ok, "attrs", attrs)

	resp = ih.printerAttributes(p)
	return
}

func (ih *basicIPPServer) printerFromRequest(req *goipp.Message) (Printer, error) {
	strName, err := extractValue[goipp.String](req.Operation, "printer-uri")
	if err != nil {
		return nil, err
	}
	printerURI := strName.String()
	if printerURI == "" {
		return nil, fmt.Errorf("printer-uri is empty in request")
	}
	uri, err := url.Parse(printerURI)
	if err != nil {
		return nil, fmt.Errorf("failed to parse printer-uri %q: %w", printerURI, err)
	}
	if uri.Scheme != "ipp" && uri.Scheme != "ipps" {
		return nil, fmt.Errorf("printer-uri %q has unsupported scheme %q, expected 'ipp' or 'ipps'", printerURI, uri.Scheme)
	}
	// Extract the printer name from the URI path
	printerName := strings.TrimPrefix(uri.Path, ih.baseURL)
	if printerName == "" || printerName == "/" {
		return nil, fmt.Errorf("printer-uri %q has no printer name in path", printerURI)
	}
	slog.Debug("printer URI parsed", "printer_name", printerName, "uri", printerURI)

	if p, ok := ih.Printer[printerName]; ok {
		return p, nil
	}
	return nil, fmt.Errorf("printer %q not found", printerURI)
}

func (ih *basicIPPServer) handleWithBaseResponse(ctx context.Context, req *goipp.Message, _ []byte) (resp *goipp.Message, err error) {
	return baseResponse(scSuccessful), nil
}

func (ih *basicIPPServer) handleGetJobAttributes(ctx context.Context, req *goipp.Message, _ []byte) (resp *goipp.Message, err error) {
	// find job id in operation attributes
	v, err := extractValue[goipp.Integer](req.Operation, "job-id")
	if err != nil {
		return resp, fmt.Errorf("failed to extract job-id: %w", err)
	}
	jobID := JobID(v)
	if jobID == 0 {
		return nil, fmt.Errorf("job-id not provided in request")
	}
	job, err := ih.spool.GetJob(jobID)
	if err != nil {
		return nil, fmt.Errorf("failed to get job with ID %d: %w", jobID, err)
	}

	resp = goipp.NewResponse(goipp.DefaultVersion, codeOK, requestNum)
	resp.Operation = job.attributes()
	return resp, nil
}

// ref: https://datatracker.ietf.org/doc/html/rfc8011#section-4.2.1.1
func (ih *basicIPPServer) handlePrintJob(ctx context.Context, req *goipp.Message, body []byte) (resp *goipp.Message, err error) {
	p, err := ih.printerFromRequest(req)
	if err != nil {
		return nil, fmt.Errorf("failed to get printer: %w", err)
	}
	j, err := createJobFromRequest(p, ih.baseURL, JobID(time.Now().Unix()), req)
	if err != nil {
		return nil, fmt.Errorf("failed to create job: %w", err)
	}
	if err := ih.spool.AddJob(ctx, j, body); err != nil {
		return nil, fmt.Errorf("failed to add job to spool: %w", err)
	}
	return baseResponse(scSuccessful), nil
}
