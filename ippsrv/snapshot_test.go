package ippsrv

import (
	"context"
	"testing"
	"time"
)

func TestJobSnapshotCopiesMutableState(t *testing.T) {
	printer := mustWrapDriver(t, testDriver{}, "test-printer", "Test Printer")
	job := mustCreateJob(t, printer, 42, "test-job")
	job.StateReasons = []JobStateReason{JSRJobIncoming}

	snap := job.Snapshot()
	snap.StateReasons[0] = JSRAbortedBySystem

	got := job.Snapshot()
	if got.StateReasons[0] != JSRJobIncoming {
		t.Fatalf("job state reasons mutated through snapshot: got %v", got.StateReasons)
	}
}

func TestServerSnapshotCopiesJobsAndPrinters(t *testing.T) {
	printer := mustWrapDriver(t, testDriver{}, "test-printer", "Test Printer")
	server, err := New(printer, WithDebug(true), WithDumpDir(t.TempDir()))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() {
		if err := server.Shutdown(context.Background()); err != nil {
			t.Fatalf("Shutdown: %v", err)
		}
	})

	job := mustCreateJob(t, printer, 7, "dashboard-test")
	job.Created = time.Unix(100, 0)
	sp, ok := server.is.spool.(*spool)
	if !ok {
		t.Fatalf("spool type = %T, want *spool", server.is.spool)
	}
	registerJob(t, sp, job)

	snap := server.Snapshot()
	if snap.BaseURL != "/printers/" {
		t.Fatalf("BaseURL = %q, want /printers/", snap.BaseURL)
	}
	if !snap.Debug || snap.DumpDir == "" {
		t.Fatalf("debug snapshot = %t, dumpdir = %q", snap.Debug, snap.DumpDir)
	}
	if len(snap.Printers) != 1 || snap.Printers[0].Name != "test-printer" {
		t.Fatalf("printers = %+v", snap.Printers)
	}
	if len(snap.Jobs) != 1 || snap.Jobs[0].ID != 7 || snap.Jobs[0].Name != "dashboard-test" {
		t.Fatalf("jobs = %+v", snap.Jobs)
	}

	snap.Jobs[0].StateReasons = append(snap.Jobs[0].StateReasons, JSRAbortedBySystem)
	got := server.Snapshot()
	if len(got.Jobs[0].StateReasons) != len(job.StateReasons) {
		t.Fatalf("snapshot exposed mutable job reasons: got %v", got.Jobs[0].StateReasons)
	}
}
