package ippsrv

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"testing"
	"time"

	"github.com/rusq/thermoprint"
)

func newTestSpool(t *testing.T) *spool {
	t.Helper()

	sp, err := newSpool(t.TempDir())
	if err != nil {
		t.Fatalf("newSpool: %v", err)
	}
	t.Cleanup(func() {
		if err := sp.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}
	})
	return sp
}

func mustWrapDriver(t *testing.T, d Driver, name, fullname string) Printer {
	t.Helper()

	p, err := WrapDriver(d, name, fullname)
	if err != nil {
		t.Fatalf("WrapDriver: %v", err)
	}
	return p
}

func mustCreateJob(t *testing.T, p Printer, id JobID, name string) *Job {
	t.Helper()

	printerURI := "ipp://localhost/printers/" + p.Name()
	jobURI := fmt.Sprintf("/printers/%s/%d", p.Name(), id)
	job, err := createJob(p, id, printerURI, jobURI, name, "tester")
	if err != nil {
		t.Fatalf("createJob: %v", err)
	}
	return job
}

// registerJob adds the job to the spool indexes without writing a job file or
// triggering processing.
func registerJob(t *testing.T, sp *spool, job *Job) {
	t.Helper()

	sp.mu.Lock()
	defer sp.mu.Unlock()
	if err := sp.addJobLocked(job); err != nil {
		t.Fatalf("addJobLocked: %v", err)
	}
}

func assertJobGone(t *testing.T, sp *spool, jobID JobID, prnName string) {
	t.Helper()

	if _, err := sp.GetJob(jobID); !errors.Is(err, errJobNotFound) {
		t.Fatalf("GetJob error = %v, want %v", err, errJobNotFound)
	}
	if got := sp.GetJobCount(prnName); got != 0 {
		t.Fatalf("GetJobCount = %d, want 0", got)
	}
}

// startAddJob runs sp.AddJob in a goroutine and returns the channel carrying
// its result.
func startAddJob(sp *spool, job *Job, data []byte) <-chan error {
	addErr := make(chan error, 1)
	go func() {
		addErr <- sp.AddJob(context.Background(), job, data)
	}()
	return addErr
}

// waitStarted waits until the driver reports that printing has started.
func waitStarted(t *testing.T, entered <-chan struct{}, addErr <-chan error) {
	t.Helper()

	select {
	case <-entered:
	case err := <-addErr:
		t.Fatalf("AddJob returned before printing started: %v", err)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for printing to start")
	}
}

// assertNotStarted asserts that printing does not start while another print
// is in progress.
func assertNotStarted(t *testing.T, entered <-chan struct{}, msg string) {
	t.Helper()

	select {
	case <-entered:
		t.Fatal(msg)
	case <-time.After(100 * time.Millisecond):
	}
}

// waitDone waits for a started AddJob to finish successfully.
func waitDone(t *testing.T, addErr <-chan error) {
	t.Helper()

	select {
	case err := <-addErr:
		if err != nil {
			t.Fatalf("AddJob: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for AddJob to finish")
	}
}

func TestSpoolRemoveJobRemovesFileAndIndexes(t *testing.T) {
	sp := newTestSpool(t)
	printer := mustWrapDriver(t, testDriver{}, "test-printer", "Test Printer")
	job := mustCreateJob(t, printer, 42, "test-job")
	registerJob(t, sp, job)

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
	assertJobGone(t, sp, job.ID, printer.Name())
}

func TestSpoolRemoveJobUnknownJobWrapsNotFound(t *testing.T) {
	sp := newTestSpool(t)

	if err := sp.RemoveJob(404); !errors.Is(err, errJobNotFound) {
		t.Fatalf("RemoveJob error = %v, want %v", err, errJobNotFound)
	}
}

func TestSpoolRemoveJobMissingFileStillRemovesJob(t *testing.T) {
	sp := newTestSpool(t)
	printer := mustWrapDriver(t, testDriver{}, "test-printer", "Test Printer")
	job := mustCreateJob(t, printer, 42, "test-job")
	registerJob(t, sp, job)

	// No job file was ever written; removal must still succeed.
	if err := sp.RemoveJob(job.ID); err != nil {
		t.Fatalf("RemoveJob: %v", err)
	}
	assertJobGone(t, sp, job.ID, printer.Name())
}

func TestSpoolAddJobRollsBackOnWriteFailure(t *testing.T) {
	sp := newTestSpool(t)
	printer := mustWrapDriver(t, testDriver{}, "test-printer", "Test Printer")
	job := mustCreateJob(t, printer, 42, "test-job")

	// Make the job file write fail by removing the spool directory.
	if err := os.RemoveAll(sp.dir); err != nil {
		t.Fatalf("RemoveAll: %v", err)
	}
	if err := sp.AddJob(context.Background(), job, tinyPNG(t)); err == nil {
		t.Fatal("AddJob succeeded, want write failure")
	}
	assertJobGone(t, sp, job.ID, printer.Name())

	// The same job can be re-added once the spool directory is back.
	if err := os.MkdirAll(sp.dir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := sp.AddJob(context.Background(), job, tinyPNG(t)); err != nil {
		t.Fatalf("AddJob after rollback: %v", err)
	}
}

func TestSpoolAddJobDoesNotHoldSpoolLockWhilePrinting(t *testing.T) {
	sp := newTestSpool(t)
	driver := newBlockingDriver(1)
	printer := mustWrapDriver(t, driver, "test-printer", "Test Printer")
	job := mustCreateJob(t, printer, 42, "test-job")

	addErr := startAddJob(sp, job, tinyPNG(t))
	waitStarted(t, driver.entered, addErr)

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

	close(driver.release)
	waitDone(t, addErr)
}

func TestPrinterSerializesConcurrentJobs(t *testing.T) {
	sp := newTestSpool(t)
	driver := newBlockingDriver(2)
	printer := mustWrapDriver(t, driver, "test-printer", "Test Printer")
	job1 := mustCreateJob(t, printer, 42, "test-job-1")
	job2 := mustCreateJob(t, printer, 43, "test-job-2")
	data := tinyPNG(t)

	addErr1 := startAddJob(sp, job1, data)
	waitStarted(t, driver.entered, addErr1)

	addErr2 := startAddJob(sp, job2, data)
	assertNotStarted(t, driver.entered, "second print started before first print was released")

	close(driver.release)
	waitDone(t, addErr1)
	waitDone(t, addErr2)
}

func TestPrintersWithSameNameDoNotShareJobLock(t *testing.T) {
	sp1 := newTestSpool(t)
	sp2 := newTestSpool(t)
	driver1 := newBlockingDriver(1)
	driver2 := newBlockingDriver(1)
	printer1 := mustWrapDriver(t, driver1, "same-printer-name", "Test Printer 1")
	printer2 := mustWrapDriver(t, driver2, "same-printer-name", "Test Printer 2")
	job1 := mustCreateJob(t, printer1, 42, "test-job-1")
	job2 := mustCreateJob(t, printer2, 42, "test-job-2")
	data := tinyPNG(t)

	addErr1 := startAddJob(sp1, job1, data)
	waitStarted(t, driver1.entered, addErr1)

	// The second printer must start printing while the first one is busy.
	addErr2 := startAddJob(sp2, job2, data)
	waitStarted(t, driver2.entered, addErr2)

	close(driver1.release)
	close(driver2.release)
	waitDone(t, addErr1)
	waitDone(t, addErr2)
}

func TestValuePrinterSerializesConcurrentJobs(t *testing.T) {
	sp := newTestSpool(t)
	driver := newBlockingDriver(2)
	printer := valuePrinter{
		id:     "value-printer",
		driver: driver,
	}
	job1 := mustCreateJob(t, printer, 42, "test-job-1")
	job2 := mustCreateJob(t, printer, 43, "test-job-2")
	data := []byte("print data")

	addErr1 := startAddJob(sp, job1, data)
	waitStarted(t, driver.entered, addErr1)

	addErr2 := startAddJob(sp, job2, data)
	assertNotStarted(t, driver.entered, "second value-printer job started before first job was released")

	close(driver.release)
	waitDone(t, addErr1)
	waitDone(t, addErr2)
}

// blockingDriver signals on entered when PrintImage is called and blocks
// until release is closed.
type blockingDriver struct {
	entered chan struct{}
	release chan struct{}
}

func newBlockingDriver(maxPrints int) *blockingDriver {
	return &blockingDriver{
		entered: make(chan struct{}, maxPrints),
		release: make(chan struct{}),
	}
}

func (blockingDriver) SetOptions(opt ...thermoprint.Option) error { return nil }
func (d *blockingDriver) PrintImage(ctx context.Context, img image.Image) error {
	d.entered <- struct{}{}
	select {
	case <-d.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (blockingDriver) DPI() float64 { return 203 }
func (blockingDriver) Width() int   { return 384 }

// valuePrinter is a non-pointer Printer implementation, used to exercise
// per-printer job serialization for printers other than basePrinter.
type valuePrinter struct {
	id     string
	driver *blockingDriver
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
