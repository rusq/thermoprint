package cmdserver

import (
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

func TestModeAllowsTUI(t *testing.T) {
	tests := []struct {
		name      string
		disabled  bool
		stdoutTTY bool
		stderrTTY bool
		want      bool
	}{
		{name: "interactive default", stdoutTTY: true, stderrTTY: true, want: true},
		{name: "disabled", disabled: true, stdoutTTY: true, stderrTTY: true, want: false},
		{name: "stdout redirected", stdoutTTY: false, stderrTTY: true, want: false},
		{name: "stderr redirected", stdoutTTY: true, stderrTTY: false, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := modeAllowsTUI(tt.disabled, tt.stdoutTTY, tt.stderrTTY)
			if got != tt.want {
				t.Fatalf("modeAllowsTUI() = %t, want %t", got, tt.want)
			}
		})
	}
}

func TestDashboardModelLogScrollKeys(t *testing.T) {
	m := dashboardModel{focusLogs: true}

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyUp})
	m = next.(dashboardModel)
	if m.logOffset != 1 {
		t.Fatalf("logOffset after up = %d, want 1", m.logOffset)
	}

	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = next.(dashboardModel)
	if m.logOffset != 0 {
		t.Fatalf("logOffset after down = %d, want 0", m.logOffset)
	}
}

func TestLogBufferBoundedCopy(t *testing.T) {
	buf := newLogBuffer(2)
	buf.append(logEntry{Message: "one"})
	buf.append(logEntry{Message: "two"})
	buf.append(logEntry{Message: "three"})

	entries := buf.Entries()
	if len(entries) != 2 || entries[0].Message != "two" || entries[1].Message != "three" {
		t.Fatalf("entries = %+v", entries)
	}
	entries[0].Message = "mutated"
	if got := buf.Entries()[0].Message; got != "two" {
		t.Fatalf("buffer exposed mutable entries: got %q", got)
	}
}

func TestRenderLogsTruncatesByDisplayWidth(t *testing.T) {
	logs := newLogBuffer(10)
	logs.append(logEntry{
		Time:    time.Date(2026, 7, 10, 12, 34, 56, 0, time.UTC),
		Level:   slog.LevelInfo,
		Message: "started",
		Attrs:   "job_id=42",
	})
	m := dashboardModel{
		width: 39, // visible line fits in width-logLineWidthReserve.
		logs:  logs,
	}

	got := m.renderLogs(2)
	if !strings.Contains(got, "job_id=42") {
		t.Fatalf("renderLogs() missing attrs:\n%s", got)
	}
	if strings.Contains(got, truncationMarker) {
		t.Fatalf("renderLogs() truncated a display-width-fitting log line:\n%s", got)
	}
}

func TestServerResultBroadcastsToMultipleWaiters(t *testing.T) {
	result := newServerResult()
	want := errors.New("server failed")
	errs := make(chan error, 2)

	go func() { errs <- result.wait() }()
	go func() { errs <- result.wait() }()

	result.finish(want)
	for range 2 {
		if got := <-errs; !errors.Is(got, want) {
			t.Fatalf("wait() = %v, want %v", got, want)
		}
	}
}
