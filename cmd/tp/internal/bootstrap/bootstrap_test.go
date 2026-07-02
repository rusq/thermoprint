package bootstrap

import (
	"context"
	"errors"
	"testing"

	"github.com/rusq/thermoprint/cmd/tp/internal/cfg"
)

func TestPrinterDryRunSkipsAdapterEnable(t *testing.T) {
	t.Cleanup(setDryRun(t, true))

	var calls int
	t.Cleanup(setEnableAdapter(func() error {
		calls++
		return errors.New("unexpected adapter enable")
	}))

	if _, err := Printer(context.Background()); err != nil {
		t.Fatalf("Printer() returned error: %v", err)
	}
	if calls != 0 {
		t.Fatalf("enableAdapter called %d times, want 0", calls)
	}
}

func TestPrinterNonDryRunEnablesAdapter(t *testing.T) {
	t.Cleanup(setDryRun(t, false))

	wantErr := errors.New("adapter unavailable")
	var calls int
	t.Cleanup(setEnableAdapter(func() error {
		calls++
		return wantErr
	}))

	if _, err := Printer(context.Background()); !errors.Is(err, wantErr) {
		t.Fatalf("Printer() error = %v, want error wrapping %v", err, wantErr)
	}
	if calls != 1 {
		t.Fatalf("enableAdapter called %d times, want 1", calls)
	}
}

func setDryRun(t *testing.T, dryRun bool) func() {
	t.Helper()

	original := cfg.DryRun
	cfg.DryRun = dryRun
	return func() {
		cfg.DryRun = original
	}
}

func setEnableAdapter(fn func() error) func() {
	original := enableAdapter
	enableAdapter = fn
	return func() {
		enableAdapter = original
	}
}
