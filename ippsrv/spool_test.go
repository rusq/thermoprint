package ippsrv

import (
	"errors"
	"os"
	"testing"
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
