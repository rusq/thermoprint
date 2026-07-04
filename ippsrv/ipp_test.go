package ippsrv

import (
	"context"
	"image"
	"testing"

	"github.com/OpenPrinting/goipp"
	"github.com/rusq/thermoprint"
)

const testRequestID uint32 = 0x12345678

type testDriver struct{}

func (testDriver) SetOptions(opt ...thermoprint.Option) error { return nil }
func (testDriver) PrintImage(ctx context.Context, img image.Image) error {
	return nil
}
func (testDriver) DPI() float64 { return 203 }
func (testDriver) Width() int   { return 384 }

func newTestIPPServer(t *testing.T) *basicIPPServer {
	t.Helper()

	p, err := WrapDriver(testDriver{}, "test-printer", "Test Printer")
	if err != nil {
		t.Fatalf("WrapDriver: %v", err)
	}
	s, err := newBasicIPPServer("/printers/", p)
	if err != nil {
		t.Fatalf("newBasicIPPServer: %v", err)
	}
	t.Cleanup(func() {
		if err := s.Shutdown(context.Background()); err != nil {
			t.Fatalf("Shutdown: %v", err)
		}
	})

	return s
}

func newIPPRequest(op goipp.Op, requestID uint32) *goipp.Message {
	req := goipp.NewRequest(goipp.DefaultVersion, op, requestID)
	a := adder(&req.Operation)
	a("attributes-charset", goipp.TagCharset, ippUTF8)
	a("attributes-natural-language", goipp.TagLanguage, ippENUS)
	a("printer-uri", goipp.TagURI, goipp.String("ipp://localhost/printers/test-printer"))
	return req
}

func TestBaseResponseUsesRequestID(t *testing.T) {
	resp := baseResponse(goipp.StatusOk, testRequestID)
	if resp.RequestID != testRequestID {
		t.Fatalf("RequestID = %d, want %d", resp.RequestID, testRequestID)
	}
}

func TestBaseResponseUsesStatusCode(t *testing.T) {
	for _, tt := range []struct {
		name   string
		status goipp.Status
	}{
		{name: "ok", status: goipp.StatusOk},
		{name: "internal error", status: goipp.StatusErrorInternal},
	} {
		t.Run(tt.name, func(t *testing.T) {
			resp := baseResponse(tt.status, testRequestID)
			if resp.Code != goipp.Code(tt.status) {
				t.Fatalf("Code = %v, want %v", resp.Code, goipp.Code(tt.status))
			}
		})
	}
}

func TestBaseResponseDoesNotAddStatusCodeAttribute(t *testing.T) {
	resp := baseResponse(goipp.StatusOk, testRequestID)
	if _, ok := findAttr(resp.Operation, "status-code"); ok {
		t.Fatal("baseResponse added status-code operation attribute")
	}
}

func TestHandleWithBaseResponseEchoesRequestID(t *testing.T) {
	s := newTestIPPServer(t)
	req := newIPPRequest(goipp.OpValidateJob, testRequestID)

	resp, err := s.handleWithBaseResponse(context.Background(), req, nil)
	if err != nil {
		t.Fatalf("handleWithBaseResponse: %v", err)
	}
	if resp.RequestID != req.RequestID {
		t.Fatalf("RequestID = %d, want %d", resp.RequestID, req.RequestID)
	}
	if resp.Code != goipp.Code(goipp.StatusOk) {
		t.Fatalf("Code = %v, want %v", resp.Code, goipp.Code(goipp.StatusOk))
	}
}

func TestHandleGetPrinterAttributesEchoesRequestID(t *testing.T) {
	s := newTestIPPServer(t)
	req := newIPPRequest(goipp.OpGetPrinterAttributes, testRequestID)

	resp, err := s.handleGetPrinterAttributes(context.Background(), req, nil)
	if err != nil {
		t.Fatalf("handleGetPrinterAttributes: %v", err)
	}
	if resp.RequestID != req.RequestID {
		t.Fatalf("RequestID = %d, want %d", resp.RequestID, req.RequestID)
	}
	if resp.Code != goipp.Code(goipp.StatusOk) {
		t.Fatalf("Code = %v, want %v", resp.Code, goipp.Code(goipp.StatusOk))
	}
}

func TestHandleGetJobAttributesEchoesRequestID(t *testing.T) {
	s := newTestIPPServer(t)
	p := s.Printer["test-printer"]
	job, err := createJob(p, 42, "ipp://localhost/printers/test-printer", "/printers/test-printer/42", "test-job", "tester")
	if err != nil {
		t.Fatalf("createJob: %v", err)
	}
	sp := s.spool.(*spool)
	sp.mu.Lock()
	if err := sp.addJobLocked(job); err != nil {
		sp.mu.Unlock()
		t.Fatalf("addJobLocked: %v", err)
	}
	sp.mu.Unlock()

	req := newIPPRequest(goipp.OpGetJobAttributes, testRequestID)
	a := adder(&req.Operation)
	a("job-id", goipp.TagInteger, goipp.Integer(job.ID))

	resp, err := s.handleGetJobAttributes(context.Background(), req, nil)
	if err != nil {
		t.Fatalf("handleGetJobAttributes: %v", err)
	}
	if resp.RequestID != req.RequestID {
		t.Fatalf("RequestID = %d, want %d", resp.RequestID, req.RequestID)
	}
	if resp.Code != goipp.Code(goipp.StatusOk) {
		t.Fatalf("Code = %v, want %v", resp.Code, goipp.Code(goipp.StatusOk))
	}
}

func TestHandlePrintJobReturnsJobAttributes(t *testing.T) {
	s := newTestIPPServer(t)
	req := newIPPRequest(goipp.OpPrintJob, testRequestID)
	a := adder(&req.Operation)
	a("job-name", goipp.TagName, goipp.String("test-job"))
	a("requesting-user-name", goipp.TagName, goipp.String("tester"))

	resp, err := s.handlePrintJob(context.Background(), req, tinyPNG(t))
	if err != nil {
		t.Fatalf("handlePrintJob: %v", err)
	}
	if resp.RequestID != req.RequestID {
		t.Fatalf("RequestID = %d, want %d", resp.RequestID, req.RequestID)
	}
	if resp.Code != goipp.Code(goipp.StatusOk) {
		t.Fatalf("Code = %v, want %v", resp.Code, goipp.Code(goipp.StatusOk))
	}
	if _, ok := findAttr(resp.Operation, "attributes-charset"); !ok {
		t.Fatal("missing attributes-charset operation attribute")
	}
	if _, ok := findAttr(resp.Operation, "attributes-natural-language"); !ok {
		t.Fatal("missing attributes-natural-language operation attribute")
	}

	jobID, err := extractValue[goipp.Integer](resp.Job, "job-id")
	if err != nil {
		t.Fatalf("job-id: %v", err)
	}
	if _, err := s.spool.GetJob(JobID(jobID)); err != nil {
		t.Fatalf("spooled job %d: %v", jobID, err)
	}
	for _, name := range []string{"job-uri", "job-state", "job-state-reasons"} {
		if _, ok := findAttr(resp.Job, name); !ok {
			t.Fatalf("missing %s job attribute", name)
		}
	}
}

func TestServeIPPUnsupportedOperationReturnsIPPError(t *testing.T) {
	s := newTestIPPServer(t)
	req := newIPPRequest(goipp.Op(0x1234), testRequestID)

	resp, err := s.ServeIPP(context.Background(), req, nil)
	if err != nil {
		t.Fatalf("ServeIPP: %v", err)
	}
	if resp.RequestID != req.RequestID {
		t.Fatalf("RequestID = %d, want %d", resp.RequestID, req.RequestID)
	}
	if resp.Code != goipp.Code(goipp.StatusErrorOperationNotSupported) {
		t.Fatalf("Code = %v, want %v", resp.Code, goipp.Code(goipp.StatusErrorOperationNotSupported))
	}
}
