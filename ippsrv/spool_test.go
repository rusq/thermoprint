package ippsrv

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/png"
	"os"
	"testing"
	"time"

	"github.com/rusq/thermoprint"
)

func TestSpoolRemoveJobRemovesFileAndIndexes(t *testing.T) {
	sp, err := newSpool(t.TempDir())
	if err != nil {
		t.Fatalf("newSpool: %v", err)
	}
	t.Cleanup(func() {
		if err := sp.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}
	})

	printer, err := WrapDriver(testDriver{}, "test-printer", "Test Printer")
	if err != nil {
		t.Fatalf("WrapDriver: %v", err)
	}
	job, err := createJob(printer, 42, "ipp://localhost/printers/test-printer", "/printers/test-printer/42", "test-job", "tester")
	if err != nil {
		t.Fatalf("createJob: %v", err)
	}

	sp.mu.Lock()
	if err := sp.addJobLocked(job); err != nil {
		sp.mu.Unlock()
		t.Fatalf("addJobLocked: %v", err)
	}
	sp.mu.Unlock()

	jobFile := sp.jobFilePath(job.ID)
	if err := os.WriteFile(jobFile, []byte("print data"), 0600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if err := sp.RemoveJob(job.ID); err != nil {
		t.Fatalf("RemoveJob: %v", err)
	}
	if _, err := os.Stat(jobFile); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Stat error = %v, want %v", err, os.ErrNotExist)
	}
	if _, err := sp.GetJob(job.ID); !errors.Is(err, errJobNotFound) {
		t.Fatalf("GetJob error = %v, want %v", err, errJobNotFound)
	}
	if got := sp.GetJobCount(printer.Name()); got != 0 {
		t.Fatalf("GetJobCount = %d, want 0", got)
	}
}

func TestSpoolRemoveJobUnknownJobWrapsNotFound(t *testing.T) {
	sp, err := newSpool(t.TempDir())
	if err != nil {
		t.Fatalf("newSpool: %v", err)
	}
	t.Cleanup(func() {
		if err := sp.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}
	})

	if err := sp.RemoveJob(404); !errors.Is(err, errJobNotFound) {
		t.Fatalf("RemoveJob error = %v, want %v", err, errJobNotFound)
	}
}

func TestSpoolAddJobDoesNotHoldSpoolLockWhilePrinting(t *testing.T) {
	sp, err := newSpool(t.TempDir())
	if err != nil {
		t.Fatalf("newSpool: %v", err)
	}
	t.Cleanup(func() {
		if err := sp.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}
	})

	started := make(chan struct{})
	release := make(chan struct{})
	printer, err := WrapDriver(blockingDriver{
		started: started,
		release: release,
	}, "test-printer", "Test Printer")
	if err != nil {
		t.Fatalf("WrapDriver: %v", err)
	}
	job, err := createJob(printer, 42, "ipp://localhost/printers/test-printer", "/printers/test-printer/42", "test-job", "tester")
	if err != nil {
		t.Fatalf("createJob: %v", err)
	}

	addErr := make(chan error, 1)
	go func() {
		addErr <- sp.AddJob(context.Background(), job, tinyPNG(t))
	}()

	select {
	case <-started:
	case err := <-addErr:
		t.Fatalf("AddJob returned before printing started: %v", err)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for printing to start")
	}

	lookupDone := make(chan error, 1)
	go func() {
		if got := sp.GetJobCount(printer.Name()); got != 1 {
			lookupDone <- errors.New("unexpected job count")
			return
		}
		if _, err := sp.GetJob(job.ID); err != nil {
			lookupDone <- err
			return
		}
		if _, err := sp.ListJobs(); err != nil {
			lookupDone <- err
			return
		}
		lookupDone <- nil
	}()

	select {
	case err := <-lookupDone:
		if err != nil {
			t.Fatalf("lookup while printing: %v", err)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("spool lookup blocked while printing")
	}

	close(release)

	select {
	case err := <-addErr:
		if err != nil {
			t.Fatalf("AddJob: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for AddJob to finish")
	}
}

type blockingDriver struct {
	started chan<- struct{}
	release <-chan struct{}
}

func (blockingDriver) SetOptions(opt ...thermoprint.Option) error { return nil }
func (d blockingDriver) PrintImage(ctx context.Context, img image.Image) error {
	close(d.started)
	select {
	case <-d.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (blockingDriver) DPI() float64 { return 203 }
func (blockingDriver) Width() int   { return 384 }

func tinyPNG(t *testing.T) []byte {
	t.Helper()

	img := image.NewRGBA(image.Rect(0, 0, 1, 1))
	img.Set(0, 0, color.Black)

	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("png.Encode: %v", err)
	}
	return buf.Bytes()
}
