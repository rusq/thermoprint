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
	resp := baseResponse(scSuccessful, testRequestID)
	if resp.RequestID != testRequestID {
		t.Fatalf("RequestID = %d, want %d", resp.RequestID, testRequestID)
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
}
