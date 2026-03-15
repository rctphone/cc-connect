package main

import (
	"context"
	"io"
	"log/slog"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"
)

// ANSI color codes
const (
	colorReset   = "\033[0m"
	colorRed     = "\033[31m"
	colorYellow  = "\033[33m"
	colorGreen   = "\033[32m"
	colorCyan    = "\033[36m"
	colorGray    = "\033[90m"
	colorBoldRed = "\033[1;31m"
)

// colorHandler is a slog.Handler that writes compact colored log output to a terminal.
//
// Output format:
//
//	18:05:03 INFO  telegram: connected  bot=raisa_aibot
//	18:05:03 ERROR platform command registration failed  error="..."
//
// Attributes are shown inline, dimmed, without cluttering the message.
type colorHandler struct {
	opts  slog.HandlerOptions
	w     io.Writer
	mu    sync.Mutex
	attrs []slog.Attr
	group string
}

func newColorHandler(w io.Writer, opts *slog.HandlerOptions) *colorHandler {
	if opts == nil {
		opts = &slog.HandlerOptions{}
	}
	return &colorHandler{opts: *opts, w: w}
}

func (h *colorHandler) Enabled(_ context.Context, level slog.Level) bool {
	minLevel := slog.LevelInfo
	if h.opts.Level != nil {
		minLevel = h.opts.Level.Level()
	}
	return level >= minLevel
}

func (h *colorHandler) Handle(_ context.Context, r slog.Record) error {
	var buf strings.Builder

	// Timestamp (dimmed)
	ts := r.Time.Format(time.TimeOnly) // HH:MM:SS
	buf.WriteString(colorGray)
	buf.WriteString(ts)
	buf.WriteString(colorReset)
	buf.WriteByte(' ')

	// Level (colored, fixed width)
	var levelColor, levelStr string
	switch {
	case r.Level >= slog.LevelError:
		levelColor = colorBoldRed
		levelStr = "ERROR"
	case r.Level >= slog.LevelWarn:
		levelColor = colorYellow
		levelStr = "WARN "
	case r.Level >= slog.LevelInfo:
		levelColor = colorGreen
		levelStr = "INFO "
	default:
		levelColor = colorCyan
		levelStr = "DEBUG"
	}
	buf.WriteString(levelColor)
	buf.WriteString(levelStr)
	buf.WriteString(colorReset)
	buf.WriteByte(' ')

	// Message (white/default, bold for errors)
	if r.Level >= slog.LevelError {
		buf.WriteString(colorRed)
	}
	buf.WriteString(r.Message)
	if r.Level >= slog.LevelError {
		buf.WriteString(colorReset)
	}

	// Collect all attrs (pre-added + record)
	writeAttr := func(a slog.Attr) {
		key := a.Key
		if h.group != "" {
			key = h.group + "." + key
		}
		val := a.Value.String()
		// Show "error" attr prominently
		if key == "error" {
			buf.WriteString("  ")
			buf.WriteString(colorRed)
			buf.WriteString(val)
			buf.WriteString(colorReset)
		} else {
			buf.WriteString("  ")
			buf.WriteString(colorGray)
			buf.WriteString(key)
			buf.WriteByte('=')
			buf.WriteString(val)
			buf.WriteString(colorReset)
		}
	}

	for _, a := range h.attrs {
		writeAttr(a)
	}
	r.Attrs(func(a slog.Attr) bool {
		writeAttr(a)
		return true
	})

	buf.WriteByte('\n')

	h.mu.Lock()
	defer h.mu.Unlock()
	_, err := io.WriteString(h.w, buf.String())
	return err
}

func (h *colorHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	newAttrs := make([]slog.Attr, len(h.attrs)+len(attrs))
	copy(newAttrs, h.attrs)
	copy(newAttrs[len(h.attrs):], attrs)
	return &colorHandler{opts: h.opts, w: h.w, attrs: newAttrs, group: h.group}
}

func (h *colorHandler) WithGroup(name string) slog.Handler {
	g := name
	if h.group != "" {
		g = h.group + "." + name
	}
	newAttrs := make([]slog.Attr, len(h.attrs))
	copy(newAttrs, h.attrs)
	return &colorHandler{opts: h.opts, w: h.w, attrs: newAttrs, group: g}
}

// isTerminal returns true if the file descriptor is a terminal.
func isTerminal(f *os.File) bool {
	if runtime.GOOS == "windows" {
		return false // conservative; Windows terminals vary
	}
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return (fi.Mode() & os.ModeCharDevice) != 0
}
