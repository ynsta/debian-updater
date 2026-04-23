package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// errDpkgAuditUnclean is the sentinel we pass to failOnError when dpkg --audit
// reports unresolved packages; lets the outer error chain stay typed.
var errDpkgAuditUnclean = errors.New("dpkg reports unresolved packages")

const (
	httpTimeout    = 30 * time.Second
	retryAttempts  = 3
	retryBaseDelay = 1 * time.Second
)

// retry runs op up to attempts times, doubling the delay between attempts
// (exponential backoff). Each failed attempt is logged at WARN level per
// AGENTS.md §Error Handling. The last error is returned. attempts must be ≥1.
// A cancelled ctx aborts early without sleeping through the remaining
// backoff — apt/HTTP operations must surface interrupts promptly.
func retry(ctx context.Context, attempts int, base time.Duration, op func() error) error {
	var err error

	for i := 1; i <= attempts; i++ {
		err = op()
		if err == nil {
			return nil
		}

		if ctx.Err() != nil {
			return fmt.Errorf("retry aborted: %w (last op: %w)", ctx.Err(), err)
		}

		if i == attempts {
			break
		}

		wait := base * time.Duration(1<<uint(i-1))
		slog.Warn("retryable operation failed",
			"attempt", i, "of", attempts, "wait_seconds", int(wait.Seconds()), "error", err.Error())

		select {
		case <-ctx.Done():
			return fmt.Errorf("retry aborted during backoff: %w", ctx.Err())
		case <-time.After(wait):
		}
	}

	return err
}

// allowedHosts is the closed set of remote hosts this tool may contact. It
// keeps gosec's SSRF analysis honest: rawURL passed to getURL is validated
// against this list before any request is issued.
var allowedHosts = map[string]struct{}{
	"endoflife.date": {},
	"ftp.debian.org": {},
}

func closeWithWarning(name string, closer io.Closer) {
	err := closer.Close()
	if err != nil {
		//nolint:gosec // G706: name is a fixed-site string and err comes from stdlib io.Closer; slog quotes control chars.
		slog.Warn("Failed to close resource", "name", name, "error", err.Error())
	}
}

// printLines emits a human-readable banner to stderr. Banners are user-facing
// UI (fatal diagnostic boxes) and are intentionally not routed through slog so
// that structured log output stays machine-parseable. Every call site also
// emits a slog.Error record carrying the same information as structured
// fields, which is what AGENTS.md §Logging expects diagnostics to honor.
func (app *App) printLines(lines ...string) {
	for _, line := range lines {
		_, err := fmt.Fprintln(os.Stderr, line)
		if err != nil {
			//nolint:gosec // G706: err is from os.Stderr write, no attacker-controlled data.
			slog.Warn("Failed to write banner line to stderr", "error", err.Error())

			return
		}
	}
}

func (app *App) failOnError(err error, msg string) {
	if err != nil {
		slog.Error(msg, "error", err.Error())
		// The deferred closeWithWarning on the log file would never run after
		// os.Exit, so flush explicitly to make sure the final ERROR record
		// reaches disk.
		if app.logFile != nil {
			_ = app.logFile.Sync()
		}

		os.Exit(1)
	}
}

// atomicWriteFile writes data to path via a same-directory temp file that is
// fsync'd and then renamed into place. The rename is atomic on a single POSIX
// filesystem, so readers either see the old file or the fully-written new
// file, never a half-truncated one. Critical for /etc/apt/sources.list, which
// apt will attempt to read the moment a concurrent process runs.
func atomicWriteFile(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)

	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("create temp file in %s: %w", dir, err)
	}

	tmpName := tmp.Name()

	cleanup := func() {
		_ = os.Remove(tmpName)
	}

	_, err = tmp.Write(data)
	if err != nil {
		_ = tmp.Close()

		cleanup()

		return fmt.Errorf("write temp file %s: %w", tmpName, err)
	}

	err = tmp.Chmod(perm)
	if err != nil {
		_ = tmp.Close()

		cleanup()

		return fmt.Errorf("chmod temp file %s: %w", tmpName, err)
	}

	err = tmp.Sync()
	if err != nil {
		_ = tmp.Close()

		cleanup()

		return fmt.Errorf("sync temp file %s: %w", tmpName, err)
	}

	err = tmp.Close()
	if err != nil {
		cleanup()

		return fmt.Errorf("close temp file %s: %w", tmpName, err)
	}

	//nolint:gosec // G703: path is supplied by the single-caller API inside this binary; atomicWriteFile is not exposed to external input.
	err = os.Rename(tmpName, path)
	if err != nil {
		cleanup()

		return fmt.Errorf("rename %s to %s: %w", tmpName, path, err)
	}

	return nil
}

// backupFile copies src to src+suffix (same directory, preserving permissions)
// so a subsequent atomicWriteFile can be reverted by the operator. Returns the
// backup path. A missing src is not an error — we simply skip.
func backupFile(src, suffix string) (string, error) {
	info, err := os.Stat(src)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", nil
		}

		return "", fmt.Errorf("stat %s: %w", src, err)
	}

	// #nosec G304 -- src is always a well-known system path passed by the caller.
	data, err := os.ReadFile(src)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", src, err)
	}

	backup := src + suffix

	err = atomicWriteFile(backup, data, info.Mode().Perm())
	if err != nil {
		return "", fmt.Errorf("write backup %s: %w", backup, err)
	}

	return backup, nil
}

// fetch retries app.fetcher.Get with exponential backoff. Single-attempt
// fetches belong on the Fetcher adapter; retry policy belongs to the App
// layer so tests can exercise success, failure, and exhaustion paths with a
// scripted Fetcher.
func (app *App) fetch(ctx context.Context, rawURL string) (*http.Response, error) {
	var resp *http.Response

	err := retry(ctx, retryAttempts, retryBaseDelay, func() error {
		//nolint:bodyclose // Body ownership passes to the caller of fetch, which closes it via closeWithWarning after reading the response.
		r, getErr := app.fetcher.Get(ctx, rawURL)
		if getErr != nil {
			return fmt.Errorf("fetcher: %w", getErr)
		}

		resp = r

		return nil
	})
	if err != nil {
		return nil, err
	}

	return resp, nil
}
