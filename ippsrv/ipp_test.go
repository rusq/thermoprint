package ippsrv

import (
	"context"
	"errors"
	"fmt"
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

func removeOperationAttr(req *goipp.Message, name string) {
	filtered := req.Operation[:0]
	for _, attr := range req.Operation {
		if attr.Name != name {
			filtered = append(filtered, attr)
		}
	}
	req.Operation = filtered
}

func addTestJob(t *testing.T, s *basicIPPServer, id JobID, name, username string) *Job {
	t.Helper()

	p := s.Printer["test-printer"]
	jobURL := fmt.Sprintf("/printers/test-printer/%d", id)
	job, err := createJob(p, id, "ipp://localhost/printers/test-printer", jobURL, name, username)
	if err != nil {
		t.Fatalf("createJob: %v", err)
	}
	job.Processing = job.Created
	job.Completed = job.Created
	sp := s.spool.(*spool)
	sp.mu.Lock()
	if err := sp.addJobLocked(job); err != nil {
		sp.mu.Unlock()
		t.Fatalf("addJobLocked: %v", err)
	}
	sp.mu.Unlock()

	return job
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
	job := addTestJob(t, s, 42, "test-job", "tester")

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
	if _, ok := findAttr(resp.Operation, "attributes-charset"); !ok {
		t.Fatal("missing attributes-charset operation attribute")
	}
	if _, ok := findAttr(resp.Operation, "attributes-natural-language"); !ok {
		t.Fatal("missing attributes-natural-language operation attribute")
	}
	if _, ok := findAttr(resp.Operation, "job-id"); ok {
		t.Fatal("job-id is in operation attributes")
	}
	jobID, err := extractValue[goipp.Integer](resp.Job, "job-id")
	if err != nil {
		t.Fatalf("job-id: %v", err)
	}
	if JobID(jobID) != job.ID {
		t.Fatalf("job-id = %d, want %d", jobID, job.ID)
	}
}

func TestHandleGetJobsReturnsSeparateJobGroups(t *testing.T) {
	s := newTestIPPServer(t)
	job1 := addTestJob(t, s, 42, "first-job", "tester")
	job2 := addTestJob(t, s, 43, "second-job", "tester")

	req := newIPPRequest(goipp.OpGetJobs, testRequestID)
	resp, err := s.handleGetJobs(context.Background(), req, nil)
	if err != nil {
		t.Fatalf("handleGetJobs: %v", err)
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
	if _, ok := findAttr(resp.Operation, "job-id"); ok {
		t.Fatal("job-id is in operation attributes")
	}

	groups := resp.AttrGroups()
	if len(groups) != 3 {
		t.Fatalf("len(AttrGroups()) = %d, want 3", len(groups))
	}
	if groups[0].Tag != goipp.TagOperationGroup {
		t.Fatalf("groups[0].Tag = %v, want %v", groups[0].Tag, goipp.TagOperationGroup)
	}
	if _, ok := findAttr(groups[0].Attrs, "job-id"); ok {
		t.Fatal("job-id is in encoded operation group")
	}
	for i, job := range []*Job{job1, job2} {
		group := groups[i+1]
		if group.Tag != goipp.TagJobGroup {
			t.Fatalf("groups[%d].Tag = %v, want %v", i+1, group.Tag, goipp.TagJobGroup)
		}
		jobID, err := extractValue[goipp.Integer](group.Attrs, "job-id")
		if err != nil {
			t.Fatalf("groups[%d] job-id: %v", i+1, err)
		}
		if JobID(jobID) != job.ID {
			t.Fatalf("groups[%d] job-id = %d, want %d", i+1, jobID, job.ID)
		}
		jobName, err := extractValue[goipp.String](group.Attrs, "job-name")
		if err != nil {
			t.Fatalf("groups[%d] job-name: %v", i+1, err)
		}
		if jobName.String() != job.Name {
			t.Fatalf("groups[%d] job-name = %q, want %q", i+1, jobName, job.Name)
		}
		jobURI, err := extractValue[goipp.String](group.Attrs, "job-uri")
		if err != nil {
			t.Fatalf("groups[%d] job-uri: %v", i+1, err)
		}
		if jobURI.String() != job.JobURI {
			t.Fatalf("groups[%d] job-uri = %q, want %q", i+1, jobURI, job.JobURI)
		}
	}

	encoded, err := resp.EncodeBytes()
	if err != nil {
		t.Fatalf("EncodeBytes: %v", err)
	}
	var decoded goipp.Message
	if err := decoded.DecodeBytes(encoded); err != nil {
		t.Fatalf("DecodeBytes: %v", err)
	}
	decodedGroups := decoded.AttrGroups()
	if len(decodedGroups) != 3 {
		t.Fatalf("decoded len(AttrGroups()) = %d, want 3", len(decodedGroups))
	}
	for i, tag := range []goipp.Tag{goipp.TagOperationGroup, goipp.TagJobGroup, goipp.TagJobGroup} {
		if decodedGroups[i].Tag != tag {
			t.Fatalf("decoded groups[%d].Tag = %v, want %v", i, decodedGroups[i].Tag, tag)
		}
	}
	for i, job := range []*Job{job1, job2} {
		jobID, err := extractValue[goipp.Integer](decodedGroups[i+1].Attrs, "job-id")
		if err != nil {
			t.Fatalf("decoded groups[%d] job-id: %v", i+1, err)
		}
		if JobID(jobID) != job.ID {
			t.Fatalf("decoded groups[%d] job-id = %d, want %d", i+1, jobID, job.ID)
		}
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

func TestServeIPPMapsClientErrorsToIPPStatus(t *testing.T) {
	for _, tt := range []struct {
		name   string
		op     goipp.Op
		mutate func(*goipp.Message)
		want   goipp.Status
	}{
		{
			name: "missing job-id is bad request",
			op:   goipp.OpGetJobAttributes,
			want: goipp.StatusErrorBadRequest,
		},
		{
			name: "unknown job is not found",
			op:   goipp.OpGetJobAttributes,
			mutate: func(req *goipp.Message) {
				a := adder(&req.Operation)
				a("job-id", goipp.TagInteger, goipp.Integer(404))
			},
			want: goipp.StatusErrorNotFound,
		},
		{
			name: "missing printer-uri is bad request",
			op:   goipp.OpGetPrinterAttributes,
			mutate: func(req *goipp.Message) {
				removeOperationAttr(req, "printer-uri")
			},
			want: goipp.StatusErrorBadRequest,
		},
		{
			name: "malformed printer-uri is bad request",
			op:   goipp.OpGetPrinterAttributes,
			mutate: func(req *goipp.Message) {
				removeOperationAttr(req, "printer-uri")
				a := adder(&req.Operation)
				a("printer-uri", goipp.TagURI, goipp.String("%"))
			},
			want: goipp.StatusErrorBadRequest,
		},
		{
			name: "unknown printer is not found",
			op:   goipp.OpGetPrinterAttributes,
			mutate: func(req *goipp.Message) {
				removeOperationAttr(req, "printer-uri")
				a := adder(&req.Operation)
				a("printer-uri", goipp.TagURI, goipp.String("ipp://localhost/printers/missing"))
			},
			want: goipp.StatusErrorNotFound,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			s := newTestIPPServer(t)
			req := newIPPRequest(tt.op, testRequestID)
			if tt.mutate != nil {
				tt.mutate(req)
			}

			resp, err := s.ServeIPP(context.Background(), req, nil)
			if err != nil {
				t.Fatalf("ServeIPP: %v", err)
			}
			if resp.RequestID != req.RequestID {
				t.Fatalf("RequestID = %d, want %d", resp.RequestID, req.RequestID)
			}
			if resp.Code != goipp.Code(tt.want) {
				t.Fatalf("Code = %v, want %v", resp.Code, goipp.Code(tt.want))
			}
		})
	}
}

func TestIPPStatusFromErrorDefaultsToInternal(t *testing.T) {
	if got := ippStatusFromError(errors.New("boom")); got != goipp.StatusErrorInternal {
		t.Fatalf("ippStatusFromError = %v, want %v", got, goipp.StatusErrorInternal)
	}
}
