package cmdserver

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"time"
)

type logEntry struct {
	Time    time.Time
	Level   slog.Level
	Message string
	Attrs   string
}

type logBuffer struct {
	mu      sync.RWMutex
	max     int
	entries []logEntry
}

func newLogBuffer(max int) *logBuffer {
	return &logBuffer{max: max}
}

func (b *logBuffer) append(e logEntry) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if e.Time.IsZero() {
		e.Time = time.Now()
	}
	b.entries = append(b.entries, e)
	if len(b.entries) > b.max {
		copy(b.entries, b.entries[len(b.entries)-b.max:])
		b.entries = b.entries[:b.max]
	}
}

func (b *logBuffer) Entries() []logEntry {
	b.mu.RLock()
	defer b.mu.RUnlock()
	out := make([]logEntry, len(b.entries))
	copy(out, b.entries)
	return out
}

func (b *logBuffer) Writer() io.Writer {
	return logWriter{buffer: b}
}

type logWriter struct {
	buffer *logBuffer
}

func (w logWriter) Write(p []byte) (int, error) {
	for line := range strings.SplitSeq(strings.TrimRight(string(p), "\n"), "\n") {
		if line != "" {
			w.buffer.append(logEntry{Time: time.Now(), Level: slog.LevelInfo, Message: line})
		}
	}
	return len(p), nil
}

type bufferHandler struct {
	buffer *logBuffer
	level  slog.Leveler
	attrs  []slog.Attr
	group  string
}

func newBufferHandler(buffer *logBuffer, level slog.Leveler) *bufferHandler {
	return &bufferHandler{buffer: buffer, level: level}
}

func (h *bufferHandler) Enabled(_ context.Context, level slog.Level) bool {
	return level >= h.level.Level()
}

func (h *bufferHandler) Handle(_ context.Context, r slog.Record) error {
	var attrs []slog.Attr
	attrs = append(attrs, h.attrs...)
	r.Attrs(func(a slog.Attr) bool {
		attrs = append(attrs, a)
		return true
	})
	h.buffer.append(logEntry{
		Time:    r.Time,
		Level:   r.Level,
		Message: r.Message,
		Attrs:   formatAttrs(attrs),
	})
	return nil
}

func (h *bufferHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	cp := *h
	cp.attrs = append(append([]slog.Attr{}, h.attrs...), attrs...)
	return &cp
}

func (h *bufferHandler) WithGroup(name string) slog.Handler {
	cp := *h
	if cp.group == "" {
		cp.group = name
	} else {
		cp.group += "." + name
	}
	return &cp
}

func formatAttrs(attrs []slog.Attr) string {
	if len(attrs) == 0 {
		return ""
	}
	var buf bytes.Buffer
	for i, a := range attrs {
		a.Value = a.Value.Resolve()
		if i > 0 {
			buf.WriteByte(' ')
		}
		buf.WriteString(a.Key)
		buf.WriteByte('=')
		buf.WriteString(fmt.Sprint(a.Value.Any()))
	}
	return buf.String()
}

type teeHandler struct {
	a slog.Handler
	b slog.Handler
}

func (h teeHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.a.Enabled(ctx, level) || h.b.Enabled(ctx, level)
}

func (h teeHandler) Handle(ctx context.Context, r slog.Record) error {
	var err error
	if h.a.Enabled(ctx, r.Level) {
		err = h.a.Handle(ctx, r.Clone())
	}
	if h.b.Enabled(ctx, r.Level) {
		if berr := h.b.Handle(ctx, r.Clone()); err == nil {
			err = berr
		}
	}
	return err
}

func (h teeHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return teeHandler{a: h.a.WithAttrs(attrs), b: h.b.WithAttrs(attrs)}
}

func (h teeHandler) WithGroup(name string) slog.Handler {
	return teeHandler{a: h.a.WithGroup(name), b: h.b.WithGroup(name)}
}

func installTUILogger(buffer *logBuffer, keepCurrent bool) {
	level := slog.LevelInfo
	if cfgVerbose() {
		level = slog.LevelDebug
	}
	tuiHandler := newBufferHandler(buffer, level)
	if keepCurrent {
		slog.SetDefault(slog.New(teeHandler{a: slog.Default().Handler(), b: tuiHandler}))
		return
	}
	slog.SetDefault(slog.New(tuiHandler))
}

func cfgVerbose() bool {
	return slog.Default().Enabled(context.Background(), slog.LevelDebug)
}
