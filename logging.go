package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"io"
	"log/slog"
	"os"
)

// multiHandler fans a slog.Record out to several handlers so text can go to
// the TTY while JSON is persisted to the log file. The stdlib ships no
// equivalent, but the interface is small enough that a local implementation is
// cheaper than a third-party dependency.
type multiHandler []slog.Handler

func newMultiHandler(handlers ...slog.Handler) slog.Handler {
	return multiHandler(handlers)
}

func (m multiHandler) Enabled(ctx context.Context, level slog.Level) bool {
	for _, h := range m {
		if h.Enabled(ctx, level) {
			return true
		}
	}

	return false
}

func (m multiHandler) Handle(ctx context.Context, record slog.Record) error {
	var firstErr error

	for _, h := range m {
		if !h.Enabled(ctx, record.Level) {
			continue
		}

		err := h.Handle(ctx, record.Clone())
		if err != nil && firstErr == nil {
			firstErr = err
		}
	}

	return firstErr
}

func (m multiHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	out := make(multiHandler, len(m))
	for i, h := range m {
		out[i] = h.WithAttrs(attrs)
	}

	return out
}

func (m multiHandler) WithGroup(name string) slog.Handler {
	out := make(multiHandler, len(m))
	for i, h := range m {
		out[i] = h.WithGroup(name)
	}

	return out
}

// isTerminal reports whether the file is attached to a character device. It
// drives the choice between the TTY-friendly text handler and the structured
// JSON handler on stdout.
func isTerminal(f *os.File) bool {
	fi, err := f.Stat()
	if err != nil {
		return false
	}

	return fi.Mode()&os.ModeCharDevice != 0
}

// generateRunID returns 16 hex characters from crypto/rand, or a conservative
// fallback if the OS RNG is unavailable. A non-unique run_id is harmless for
// correctness; it only degrades log correlation.
func generateRunID() string {
	var buf [8]byte

	_, err := rand.Read(buf[:])
	if err != nil {
		return "0000000000000000"
	}

	return hex.EncodeToString(buf[:])
}

// newLogger wires the structured logger required by AGENTS.md: JSON to the
// log file, text to a TTY (JSON otherwise), and a set of base attributes
// pinned to every record.
func newLogger(stdout *os.File, fileWriter io.Writer, runID, host string) *slog.Logger {
	opts := &slog.HandlerOptions{Level: slog.LevelInfo}

	var stdoutHandler slog.Handler
	if isTerminal(stdout) {
		stdoutHandler = slog.NewTextHandler(stdout, opts)
	} else {
		stdoutHandler = slog.NewJSONHandler(stdout, opts)
	}

	fileHandler := slog.NewJSONHandler(fileWriter, opts)

	base := slog.New(newMultiHandler(stdoutHandler, fileHandler))

	return base.With(
		slog.String("service", "debian-updater"),
		slog.String("run_id", runID),
		slog.String("host", host),
	)
}
