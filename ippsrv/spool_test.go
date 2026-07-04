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

func TestPrinterSerializesConcurrentJobs(t *testing.T) {
	sp, err := newSpool(t.TempDir())
	if err != nil {
		t.Fatalf("newSpool: %v", err)
	}
	t.Cleanup(func() {
		if err := sp.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}
	})

	driver := &serialBlockingDriver{
		entered: make(chan struct{}, 2),
		release: make(chan struct{}),
	}
	printer, err := WrapDriver(driver, "test-printer", "Test Printer")
	if err != nil {
		t.Fatalf("WrapDriver: %v", err)
	}
	job1, err := createJob(printer, 42, "ipp://localhost/printers/test-printer", "/printers/test-printer/42", "test-job-1", "tester")
	if err != nil {
		t.Fatalf("createJob job1: %v", err)
	}
	job2, err := createJob(printer, 43, "ipp://localhost/printers/test-printer", "/printers/test-printer/43", "test-job-2", "tester")
	if err != nil {
		t.Fatalf("createJob job2: %v", err)
	}
	data := tinyPNG(t)

	addErr1 := make(chan error, 1)
	go func() {
		addErr1 <- sp.AddJob(context.Background(), job1, data)
	}()

	select {
	case <-driver.entered:
	case err := <-addErr1:
		t.Fatalf("first AddJob returned before printing started: %v", err)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for first print to start")
	}

	addErr2 := make(chan error, 1)
	go func() {
		addErr2 <- sp.AddJob(context.Background(), job2, data)
	}()

	select {
	case <-driver.entered:
		t.Fatal("second print started before first print was released")
	case <-time.After(100 * time.Millisecond):
	}

	close(driver.release)

	select {
	case err := <-addErr1:
		if err != nil {
			t.Fatalf("first AddJob: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for first AddJob to finish")
	}
	select {
	case err := <-addErr2:
		if err != nil {
			t.Fatalf("second AddJob: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for second AddJob to finish")
	}
}

func TestPrintersWithSameNameDoNotShareJobLock(t *testing.T) {
	sp1, err := newSpool(t.TempDir())
	if err != nil {
		t.Fatalf("newSpool sp1: %v", err)
	}
	t.Cleanup(func() {
		if err := sp1.Close(); err != nil {
			t.Fatalf("Close sp1: %v", err)
		}
	})
	sp2, err := newSpool(t.TempDir())
	if err != nil {
		t.Fatalf("newSpool sp2: %v", err)
	}
	t.Cleanup(func() {
		if err := sp2.Close(); err != nil {
			t.Fatalf("Close sp2: %v", err)
		}
	})

	driver1 := &serialBlockingDriver{
		entered: make(chan struct{}, 1),
		release: make(chan struct{}),
	}
	driver2 := &serialBlockingDriver{
		entered: make(chan struct{}, 1),
		release: make(chan struct{}),
	}
	printer1, err := WrapDriver(driver1, "same-printer-name", "Test Printer 1")
	if err != nil {
		t.Fatalf("WrapDriver printer1: %v", err)
	}
	printer2, err := WrapDriver(driver2, "same-printer-name", "Test Printer 2")
	if err != nil {
		t.Fatalf("WrapDriver printer2: %v", err)
	}
	job1, err := createJob(printer1, 42, "ipp://localhost/printers/same-printer-name", "/printers/same-printer-name/42", "test-job-1", "tester")
	if err != nil {
		t.Fatalf("createJob job1: %v", err)
	}
	job2, err := createJob(printer2, 42, "ipp://localhost/printers/same-printer-name", "/printers/same-printer-name/42", "test-job-2", "tester")
	if err != nil {
		t.Fatalf("createJob job2: %v", err)
	}
	data := tinyPNG(t)

	addErr1 := make(chan error, 1)
	go func() {
		addErr1 <- sp1.AddJob(context.Background(), job1, data)
	}()

	select {
	case <-driver1.entered:
	case err := <-addErr1:
		t.Fatalf("first AddJob returned before printing started: %v", err)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for first print to start")
	}

	addErr2 := make(chan error, 1)
	go func() {
		addErr2 <- sp2.AddJob(context.Background(), job2, data)
	}()

	select {
	case <-driver2.entered:
	case err := <-addErr2:
		t.Fatalf("second AddJob returned before printing started: %v", err)
	case <-time.After(time.Second):
		t.Fatal("second printer with same name was blocked by first printer")
	}

	close(driver1.release)
	close(driver2.release)

	select {
	case err := <-addErr1:
		if err != nil {
			t.Fatalf("first AddJob: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for first AddJob to finish")
	}
	select {
	case err := <-addErr2:
		if err != nil {
			t.Fatalf("second AddJob: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for second AddJob to finish")
	}
}

func TestValuePrinterSerializesConcurrentJobs(t *testing.T) {
	sp, err := newSpool(t.TempDir())
	if err != nil {
		t.Fatalf("newSpool: %v", err)
	}
	t.Cleanup(func() {
		if err := sp.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}
	})

	driver := &serialBlockingDriver{
		entered: make(chan struct{}, 2),
		release: make(chan struct{}),
	}
	printer := valuePrinter{
		id:     "value-printer",
		driver: driver,
	}
	job1, err := createJob(printer, 42, "ipp://localhost/printers/value-printer", "/printers/value-printer/42", "test-job-1", "tester")
	if err != nil {
		t.Fatalf("createJob job1: %v", err)
	}
	job2, err := createJob(printer, 43, "ipp://localhost/printers/value-printer", "/printers/value-printer/43", "test-job-2", "tester")
	if err != nil {
		t.Fatalf("createJob job2: %v", err)
	}

	addErr1 := make(chan error, 1)
	go func() {
		addErr1 <- sp.AddJob(context.Background(), job1, []byte("print data"))
	}()

	select {
	case <-driver.entered:
	case err := <-addErr1:
		t.Fatalf("first AddJob returned before printing started: %v", err)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for first print to start")
	}

	addErr2 := make(chan error, 1)
	go func() {
		addErr2 <- sp.AddJob(context.Background(), job2, []byte("print data"))
	}()

	select {
	case <-driver.entered:
		t.Fatal("second value-printer job started before first job was released")
	case <-time.After(100 * time.Millisecond):
	}

	close(driver.release)

	select {
	case err := <-addErr1:
		if err != nil {
			t.Fatalf("first AddJob: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for first AddJob to finish")
	}
	select {
	case err := <-addErr2:
		if err != nil {
			t.Fatalf("second AddJob: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for second AddJob to finish")
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

type serialBlockingDriver struct {
	entered chan struct{}
	release chan struct{}
}

func (serialBlockingDriver) SetOptions(opt ...thermoprint.Option) error { return nil }
func (d *serialBlockingDriver) PrintImage(ctx context.Context, img image.Image) error {
	d.entered <- struct{}{}
	select {
	case <-d.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (serialBlockingDriver) DPI() float64 { return 203 }
func (serialBlockingDriver) Width() int   { return 384 }

type valuePrinter struct {
	id     string
	driver *serialBlockingDriver
}

func (p valuePrinter) Name() string                { return p.id }
func (p valuePrinter) MakeAndModel() string        { return "Value Printer" }
func (p valuePrinter) Info() string                { return "Value Printer" }
func (p valuePrinter) State() PrinterState         { return PSIdle }
func (p valuePrinter) Ready() bool                 { return true }
func (p valuePrinter) UpTime() int                 { return 0 }
func (p valuePrinter) MediaSupported() []string    { return []string{"roll_57mm"} }
func (p valuePrinter) MediaDefault() string        { return "roll_57mm" }
func (p valuePrinter) SetState(state PrinterState) {}
func (p valuePrinter) UUID() string                { return p.id }
func (p valuePrinter) Driver() Driver              { return p.driver }
func (p valuePrinter) Print(ctx context.Context, data []byte) error {
	return p.driver.PrintImage(ctx, nil)
}

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
