// Package ippsrv implements a basic IPP server that handles print jobs and printer attributes.
//
// References:
//  - https://datatracker.ietf.org/doc/html/rfc8011
//  - https://datatracker.ietf.org/doc/html/rfc3510

package ippsrv

import (
	"context"
	"errors"
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

type ippStatusError struct {
	status goipp.Status
	err    error
}

func (e ippStatusError) Error() string {
	return e.err.Error()
}

func (e ippStatusError) Unwrap() error {
	return e.err
}

func ippError(status goipp.Status, format string, args ...any) error {
	return ippStatusError{
		status: status,
		err:    fmt.Errorf(format, args...),
	}
}

func ippStatusFromError(err error) goipp.Status {
	var statusErr ippStatusError
	if errors.As(err, &statusErr) {
		return statusErr.status
	}
	if errors.Is(err, errJobNotFound) {
		return goipp.StatusErrorNotFound
	}
	return goipp.StatusErrorInternal
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
		goipp.OpPrintJob:             ih.handlePrintJob,
		goipp.OpValidateJob:          ih.handleWithBaseResponse,
		goipp.OpGetJobAttributes:     ih.handleGetJobAttributes,
		goipp.OpGetJobs:              ih.handleGetJobs,
		goipp.OpGetPrinterAttributes: ih.handleGetPrinterAttributes,
		goipp.OpCupsGetPrinters:      ih.handleGetPrinterAttributes,
		goipp.OpCupsGetDefault:       ih.handleGetPrinterAttributes,
	}
	next, ok := handlers[goipp.Op(req.Code)]
	if !ok || next == nil {
		lg.Error("unsupported operation", "code", req.Code, "is_mapped", ok)
		return baseResponse(goipp.StatusErrorOperationNotSupported, req.RequestID), nil
	}
	slog.Debug("ipp request", "code", req.Code, "request_id", req.RequestID)
	resp, err = next(ctx, req, body)
	if err != nil {
		status := ippStatusFromError(err)
		lg.Error("failed to handle IPP request", "error", err, "status", status)
		return baseResponse(status, req.RequestID), nil
	}
	return resp, nil
}

func (ih *basicIPPServer) printerAttributes(p Printer, requestID uint32, printerURI string) *goipp.Message {
	if printerURI == "" {
		printerURI = ih.baseURL + p.Name()
	}
	dpi := int(p.Driver().DPI())
	m := baseResponse(goipp.StatusOk, requestID)
	a := adder(&m.Operation)
	a("printer-uri-supported", goipp.TagURI, goipp.String(printerURI))
	a("uri-authentication-supported", goipp.TagKeyword, ippNone)
	a("uri-security-supported", goipp.TagKeyword, ippNone)
	a("printer-name", goipp.TagName, goipp.String(p.Name()))
	a("printer-info", goipp.TagText, goipp.String(p.Info()))
	a("printer-make-and-model", goipp.TagText, goipp.String(p.MakeAndModel()))
	a("printer-state", goipp.TagEnum, goipp.Integer(p.State()))
	a("printer-state-reasons", goipp.TagKeyword, ippNone)
	a("ipp-versions-supported", goipp.TagKeyword, goipp.String("1.1"), goipp.String("2.0"))
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
	// Only raster formats are advertised: listing application/pdf would make
	// CUPS driverless clients pass PDFs through instead of rasterising them
	// client-side.  PDF is still accepted — the print filter sniffs the data
	// format and falls back to ImageMagick for anything that is not raster.
	a("document-format-default", goipp.TagMimeType, ippImagePWGRaster)
	a("document-format-supported", goipp.TagMimeType, ippImagePWGRaster, ippImageURF)
	// PWG 5102.4 raster attributes; type keywords are bits-per-COLOR
	// (24-bit RGB would be srgb_8), mono/grayscale only for this printer.
	a("pwg-raster-document-resolution-supported", goipp.TagResolution,
		goipp.Resolution{Xres: dpi, Yres: dpi, Units: goipp.UnitsDpi})
	a("pwg-raster-document-type-supported", goipp.TagKeyword,
		goipp.String("black_1"), goipp.String("sgray_8"))
	a("pwg-raster-document-sheet-back", goipp.TagKeyword, goipp.String("normal"))
	// Apple Raster; grayscale only (no SRGB24) for the same reason.  Must
	// match the URF TXT record key, hence the shared helper.
	a("urf-supported", goipp.TagKeyword, stringsToValues(urfSupported(dpi))...)
	a("printer-resolution-supported", goipp.TagResolution,
		goipp.Resolution{Xres: dpi, Yres: dpi, Units: goipp.UnitsDpi})
	a("printer-resolution-default", goipp.TagResolution,
		goipp.Resolution{Xres: dpi, Yres: dpi, Units: goipp.UnitsDpi})
	a("print-color-mode-supported", goipp.TagKeyword, goipp.String("monochrome"))
	a("print-color-mode-default", goipp.TagKeyword, goipp.String("monochrome"))
	a("sides-supported", goipp.TagKeyword, goipp.String("one-sided"))
	a("sides-default", goipp.TagKeyword, goipp.String("one-sided"))
	a("copies-supported", goipp.TagRange, goipp.Range{Lower: 1, Upper: 1})
	a("copies-default", goipp.TagInteger, goipp.Integer(1))
	// print-quality drives the resolution entries in Apple's ipp2ppd
	// AirPrint PPD generator: without it no *DefaultResolution is emitted
	// and cgpdftoraster rasterises at 100dpi, printing at half size.
	a("print-quality-supported", goipp.TagEnum, goipp.Integer(4)) // normal
	a("print-quality-default", goipp.TagEnum, goipp.Integer(4))
	a("printer-is-accepting-jobs", goipp.TagBoolean, goipp.Boolean(p.Ready()))
	a("queued-job-count", goipp.TagInteger, goipp.Integer(ih.spool.GetJobCount(p.Name()))) // TODO: interrogate spooler for queued jobs for this printer
	a("pdl-override-supported", goipp.TagKeyword, goipp.String("not-attempted"))
	a("printer-up-time", goipp.TagInteger, goipp.Integer(p.UpTime()))
	a("compression-supported", goipp.TagKeyword, ippNone)
	a("media-supported", goipp.TagKeyword, stringsToValues(p.MediaSupported())...)
	a("media-default", goipp.TagKeyword, goipp.String(p.MediaDefault()))
	if sizes, cols := mediaCollections(p.MediaSupported()); len(sizes) > 0 {
		a("media-size-supported", goipp.TagBeginCollection, sizes...)
		a("media-col-database", goipp.TagBeginCollection, cols...)
	}
	if x, y, err := mediaSizeDimensions(p.MediaDefault()); err == nil {
		a("media-col-default", goipp.TagBeginCollection, mediaCol(x, y))
	}
	a("printer-uuid", goipp.TagURI, goipp.String("urn:uuid:"+p.UUID()))

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

	// echo the printer-uri the client used, if any, as
	// printer-uri-supported.
	uri, _ := extractValue[goipp.String](req.Operation, "printer-uri")

	resp = ih.printerAttributes(p, req.RequestID, uri.String())
	return
}

func (ih *basicIPPServer) printerFromRequest(req *goipp.Message) (Printer, error) {
	strName, err := extractValue[goipp.String](req.Operation, "printer-uri")
	if err != nil {
		return nil, ippError(goipp.StatusErrorBadRequest, "invalid printer-uri: %w", err)
	}
	printerURI := strName.String()
	if printerURI == "" {
		return nil, ippError(goipp.StatusErrorBadRequest, "printer-uri is empty in request")
	}
	uri, err := url.Parse(printerURI)
	if err != nil {
		return nil, ippError(goipp.StatusErrorBadRequest, "failed to parse printer-uri %q: %w", printerURI, err)
	}
	if uri.Scheme != "ipp" && uri.Scheme != "ipps" {
		return nil, ippError(goipp.StatusErrorBadRequest, "printer-uri %q has unsupported scheme %q, expected 'ipp' or 'ipps'", printerURI, uri.Scheme)
	}
	// Extract the printer name from the URI path
	printerName := strings.TrimPrefix(uri.Path, ih.baseURL)
	if printerName == "" || printerName == "/" {
		return nil, ippError(goipp.StatusErrorBadRequest, "printer-uri %q has no printer name in path", printerURI)
	}
	slog.Debug("printer URI parsed", "printer_name", printerName, "uri", printerURI)

	if p, ok := ih.Printer[printerName]; ok {
		return p, nil
	}
	return nil, ippError(goipp.StatusErrorNotFound, "printer %q not found", printerURI)
}

func (ih *basicIPPServer) handleWithBaseResponse(ctx context.Context, req *goipp.Message, _ []byte) (resp *goipp.Message, err error) {
	return baseResponse(goipp.StatusOk, req.RequestID), nil
}

func (ih *basicIPPServer) handleGetJobAttributes(ctx context.Context, req *goipp.Message, _ []byte) (resp *goipp.Message, err error) {
	// find job id in operation attributes
	v, err := extractValue[goipp.Integer](req.Operation, "job-id")
	if err != nil {
		return resp, ippError(goipp.StatusErrorBadRequest, "failed to extract job-id: %w", err)
	}
	jobID := JobID(v)
	if jobID == 0 {
		return nil, ippError(goipp.StatusErrorBadRequest, "job-id not provided in request")
	}
	job, err := ih.spool.GetJob(jobID)
	if err != nil {
		return nil, fmt.Errorf("failed to get job with ID %d: %w", jobID, err)
	}

	resp = baseResponse(goipp.StatusOk, req.RequestID)
	resp.Job = job.attributes()
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
	resp = baseResponse(goipp.StatusOk, req.RequestID)
	resp.Job = j.attributes()
	return resp, nil
}

func asString(vv goipp.Values, ok bool) (string, bool) {
	if !ok {
		return "", false
	}
	if len(vv) == 0 {
		return "", false
	}
	v := vv[0].V
	if v.Type() != goipp.TypeString {
		return "", false
	}
	return v.String(), true
}

func (ih *basicIPPServer) handleGetJobs(ctx context.Context, req *goipp.Message, _ []byte) (*goipp.Message, error) {
	// request attributes:
	// - attributes-charset (charset)
	// - attributes-natural-language (naturalLanguage)
	// - printer-uri (uri)
	// - requesting-user-name SHOULD name(MAX)
	// - limit MAY integer(1:MAX)
	// - requested-attributes MAY 1setOf type2 keyword
	// - which-jobs MAY type2 keyword
	// - my-jobs MAY boolean
	// response attributes:
	// - attributes-charset (charset)
	// - attributes-natural-language (naturalLanguage)
	//
	p, err := ih.printerFromRequest(req)
	if err != nil {
		return nil, fmt.Errorf("failed to get printer: %w", err)
	}
	lg := slog.With("printer", p.Name(), "code", req.Code, "request_id", req.RequestID)
	username, _ := asString(findAttr(req.Operation, "requesting-user-name"))
	if username != "" {
		lg = lg.With("username", username)
	}

	// Get the requested attributes
	attrs, ok := findAttr(req.Operation, "requested-attributes")
	lg.Debug("requested attributes", "ok", ok, "attrs", attrs)

	jobs, err := ih.spool.GetJobs(p.Name())
	if err != nil {
		return nil, fmt.Errorf("failed to get jobs for printer %q: %w", p.Name(), err)
	}

	resp := baseResponse(goipp.StatusOk, req.RequestID)
	resp.Groups = goipp.Groups{
		{
			Tag:   goipp.TagOperationGroup,
			Attrs: resp.Operation,
		},
	}

	for _, job := range jobs {
		if username != "" && job.Username != username {
			continue // Skip jobs not owned by the requesting user
		}
		resp.Groups.Add(goipp.Group{
			Tag:   goipp.TagJobGroup,
			Attrs: job.attributes(),
		})
	}

	return resp, nil
}
