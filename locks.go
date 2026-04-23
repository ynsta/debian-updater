//go:build unix

package main

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"syscall"
	"time"
)

// realLockProber takes an exclusive, non-blocking flock(2) on path and
// releases immediately. A missing path is treated as free.
type realLockProber struct{}

//nolint:ireturn // DI constructor returns the LockProber interface.
func newRealLockProber() LockProber { return realLockProber{} }

func (realLockProber) Probe(path string) error { return probeFlock(path) }

// aptLockPaths are the lock files apt and dpkg obtain an exclusive flock on
// during any mutating operation. The tool must refuse to rewrite sources.list
// while any of them is held — overwriting sources out from under a running
// apt transaction yields an apt cache out of sync with dpkg's view of the
// system.
var aptLockPaths = []string{
	"/var/lib/dpkg/lock-frontend",
	"/var/lib/apt/lists/lock",
}

// ensureAptLocksFree probes each apt/dpkg lock path with a non-blocking flock
// and releases immediately. It is a liveness check, not a held lock: apt and
// dpkg themselves must be able to acquire these locks later. If the probe
// fails, the tool aborts so the operator can investigate the running apt
// session rather than racing with it.
func (app *App) ensureAptLocksFree() {
	if app.cfg.DryRun || app.lockProber == nil {
		return
	}

	const attempts = 3

	backoff := 2 * time.Second

	for _, path := range aptLockPaths {
		var lastErr error

		for attempt := 1; attempt <= attempts; attempt++ {
			err := app.lockProber.Probe(path)
			if err == nil {
				lastErr = nil

				break
			}

			lastErr = err

			if attempt == attempts {
				break
			}

			slog.Warn("apt/dpkg lock is held, retrying",
				"step", "locks.probe", "lock", path, "attempt", attempt, "backoff_seconds", int(backoff.Seconds()), "error", err.Error())

			time.Sleep(backoff)
			backoff *= 2
		}

		if lastErr != nil {
			app.failOnError(lastErr, fmt.Sprintf("apt/dpkg lock %s is held by another process (is unattended-upgrades or a user apt running?)", path))
		}
	}
}

// probeFlock tries to take an exclusive, non-blocking flock on path and
// releases it before returning. A missing lock file is treated as free: apt
// creates these files on first use, and the tool does not need to preempt
// them.
func probeFlock(path string) error {
	// #nosec G304 -- path is from a fixed allowlist of apt/dpkg lock files.
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}

		return fmt.Errorf("open lock %s: %w", path, err)
	}

	defer closeWithWarning(path, f)

	//nolint:gosec // G115: os.File.Fd returns a platform file descriptor; on Linux it always fits in an int.
	fd := int(f.Fd())

	err = syscall.Flock(fd, syscall.LOCK_EX|syscall.LOCK_NB)
	if err != nil {
		return fmt.Errorf("acquire lock %s: %w", path, err)
	}

	err = syscall.Flock(fd, syscall.LOCK_UN)
	if err != nil {
		return fmt.Errorf("release lock %s: %w", path, err)
	}

	return nil
}
