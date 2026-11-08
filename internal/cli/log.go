package cli

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
)

// logHandler prints plain CLI log lines to stderr: "message key=value".
// Info lines have no prefix; other levels are prefixed (e.g. "WARN").
type logHandler struct {
	w     io.Writer
	level slog.Level
	mu    *sync.Mutex
	attrs []slog.Attr
}

func newLogHandler(w io.Writer, level slog.Level) *logHandler {
	return &logHandler{w: w, level: level, mu: &sync.Mutex{}}
}

func (h *logHandler) Enabled(_ context.Context, l slog.Level) bool { return l >= h.level }

func (h *logHandler) Handle(_ context.Context, r slog.Record) error {
	var b strings.Builder
	if r.Level != slog.LevelInfo {
		b.WriteString(r.Level.String() + " ")
	}
	b.WriteString(r.Message)
	write := func(a slog.Attr) bool {
		fmt.Fprintf(&b, " %s=%v", a.Key, a.Value)
		return true
	}
	for _, a := range h.attrs {
		write(a)
	}
	r.Attrs(write)
	h.mu.Lock()
	defer h.mu.Unlock()
	_, err := fmt.Fprintln(h.w, b.String())
	return err
}

func (h *logHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	c := *h
	c.attrs = append(append([]slog.Attr{}, h.attrs...), attrs...)
	return &c
}

func (h *logHandler) WithGroup(string) slog.Handler { return h }
