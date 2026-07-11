package cmdserver

import (
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/rusq/thermoprint/ippsrv"
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

func TestDashboardModel(t *testing.T) {
	t.Run("Update cycles focus and scrolls", func(t *testing.T) {
		m := dashboardModel{ctx: t.Context()}
		m.serverSnap.Jobs = make([]ippsrv.JobSnapshot, 20)
		for i := range m.serverSnap.Jobs {
			m.serverSnap.Jobs[i].ID = ippsrv.JobID(i + 1)
		}

		for _, want := range []dashboardFocus{focusLogs, focusJobs, focusNone} {
			next, _ := m.Update(tea.KeyMsg{Type: tea.KeyTab})
			m = next.(dashboardModel)
			if m.focus != want {
				t.Fatalf("focus after tab = %v, want %v", m.focus, want)
			}
		}

		next, _ := m.Update(tea.KeyMsg{Type: tea.KeyUp})
		m = next.(dashboardModel)
		if m.logOffset != 0 || m.jobOffset != 0 {
			t.Fatalf("scroll changed without focus: logs=%d jobs=%d", m.logOffset, m.jobOffset)
		}

		m.focus = focusLogs

		next, _ = m.Update(tea.KeyMsg{Type: tea.KeyUp})
		m = next.(dashboardModel)
		if m.logOffset != 1 {
			t.Fatalf("logOffset after up = %d, want 1", m.logOffset)
		}

		next, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
		m = next.(dashboardModel)
		if m.logOffset != 0 {
			t.Fatalf("logOffset after down = %d, want 0", m.logOffset)
		}
		if m.jobOffset != 0 {
			t.Fatalf("jobOffset changed while logs focused: %d", m.jobOffset)
		}

		m.focus = focusJobs
		next, _ = m.Update(tea.KeyMsg{Type: tea.KeyPgUp})
		m = next.(dashboardModel)
		if m.jobOffset != scrollPageSize {
			t.Fatalf("jobOffset after pgup = %d, want %d", m.jobOffset, scrollPageSize)
		}
		m.jobOffset = m.maxJobOffset(t.Context())
		next, _ = m.Update(tea.KeyMsg{Type: tea.KeyUp})
		m = next.(dashboardModel)
		if want := m.maxJobOffset(t.Context()); m.jobOffset != want {
			t.Fatalf("jobOffset after up at oldest = %d, want %d", m.jobOffset, want)
		}
		next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnd})
		m = next.(dashboardModel)
		if m.jobOffset != 0 {
			t.Fatalf("jobOffset after end = %d, want 0", m.jobOffset)
		}
	})

	t.Run("renderJobs limits records and scrolls", func(t *testing.T) {
		m := dashboardModel{
			ctx: t.Context(),
			serverSnap: ippsrv.ServerSnapshot{Jobs: []ippsrv.JobSnapshot{
				{ID: 1, Name: "one"},
				{ID: 2, Name: "two"},
				{ID: 3, Name: "three"},
			}},
		}

		got := m.renderJobs(t.Context(), 6)
		if strings.Contains(got, "1    ") || !strings.Contains(got, "2    ") || !strings.Contains(got, "3    ") {
			t.Fatalf("renderJobs() did not show the newest two jobs:\n%s", got)
		}
		if gotLines := strings.Count(got, "\n") + 1; gotLines > 6 {
			t.Fatalf("renderJobs() rendered %d lines, want at most 6:\n%s", gotLines, got)
		}

		m.jobOffset = 10
		got = m.renderJobs(t.Context(), 6)
		if !strings.Contains(got, "1    ") || strings.Contains(got, "3    ") {
			t.Fatalf("renderJobs() did not clamp to the oldest job:\n%s", got)
		}
		if !strings.Contains(got, "newer below") {
			t.Fatalf("renderJobs() missing newer-jobs indicator:\n%s", got)
		}
	})

	t.Run("renderLogs truncates by display width", func(t *testing.T) {
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
	})
}

func TestLogBuffer(t *testing.T) {
	t.Run("Entries returns a bounded copy", func(t *testing.T) {
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
	})
}

func TestRenderLogLine(t *testing.T) {
	t.Run("shades attrs", func(t *testing.T) {
		lipgloss.SetColorProfile(termenv.TrueColor)

		line := renderLogLine(logEntry{
			Time:    time.Date(2026, 7, 10, 12, 34, 56, 0, time.UTC),
			Level:   slog.LevelInfo,
			Message: "started",
			Attrs:   "job_id=42",
		}, 80)

		if !strings.Contains(line, "job_id=42") {
			t.Fatalf("renderLogLine() missing attrs: %q", line)
		}
		if !strings.Contains(line, "\x1b[") {
			t.Fatalf("renderLogLine() attrs are not styled: %q", line)
		}
	})
}

func TestServerResult(t *testing.T) {
	t.Run("wait broadcasts to multiple waiters", func(t *testing.T) {
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
	})
}
