package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"runtime"
	"sync"
	"time"
)

// ANSI color codes
const (
	colorReset  = "\033[0m"
	colorRed    = "\033[31m"
	colorYellow = "\033[33m"
	colorGreen  = "\033[32m"
	colorCyan   = "\033[36m"
	colorGray   = "\033[90m"
)

// colorHandler is a slog.Handler that writes colored log output to a terminal.
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
	// Level with color
	var levelColor, levelStr string
	switch {
	case r.Level >= slog.LevelError:
		levelColor = colorRed
		levelStr = "ERROR"
	case r.Level >= slog.LevelWarn:
		levelColor = colorYellow
		levelStr = "WARN"
	case r.Level >= slog.LevelInfo:
		levelColor = colorGreen
		levelStr = "INFO"
	default:
		levelColor = colorCyan
		levelStr = "DEBUG"
	}

	// Format: time level msg key=value ...
	ts := r.Time.Format(time.TimeOnly) // HH:MM:SS
	line := fmt.Sprintf("%s%s%s %s%-5s%s %s",
		colorGray, ts, colorReset,
		levelColor, levelStr, colorReset,
		r.Message)

	// Append pre-added attrs
	for _, a := range h.attrs {
		line += fmt.Sprintf(" %s%s%s=%v", colorCyan, a.Key, colorReset, a.Value)
	}

	// Append record attrs
	r.Attrs(func(a slog.Attr) bool {
		key := a.Key
		if h.group != "" {
			key = h.group + "." + key
		}
		line += fmt.Sprintf(" %s%s%s=%v", colorCyan, key, colorReset, a.Value)
		return true
	})

	line += "\n"

	h.mu.Lock()
	defer h.mu.Unlock()
	_, err := io.WriteString(h.w, line)
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
