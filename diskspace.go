package main

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
)

// minFreeBytes is the per-path free-space floor below which the tool refuses
// to start an upgrade. A full /var during dpkg unpack is one of the worst
// failure modes — dpkg can leave packages in a half-configured state that is
// hard to recover without a network-attached rescue image. Five gigabytes
// covers a worst-case full-upgrade of a minimal Debian install plus a little
// slack; operators running heavier profiles should plan accordingly.
const minFreeBytes uint64 = 5 * 1024 * 1024 * 1024

// diskSpacePaths are the filesystems the tool probes before any mutation.
// /var/cache/apt is where downloaded packages land; / is where dpkg unpacks
// and installs. If either is close to full we halt.
var diskSpacePaths = []string{"/", "/var/cache/apt"}

func (app *App) checkDiskSpace() {
	slog.Info("Running pre-flight disk-space check...", "step", "preflight.disk_space", "min_free_bytes", minFreeBytes)

	low := map[string]uint64{}

	for _, path := range diskSpacePaths {
		free, err := app.fs.AvailableBytes(path)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}

			slog.Warn("Could not stat path for disk space check", "step", "preflight.disk_space", "path", path, "error", err.Error())

			continue
		}

		if free < minFreeBytes {
			low[path] = free

			slog.Error("Preflight failed: insufficient free disk space", "step", "preflight.disk_space", "path", path, "free_bytes", free, "required_bytes", minFreeBytes)
		}
	}

	if len(low) == 0 {
		slog.Info("Disk space check passed.", "step", "preflight.disk_space")

		return
	}

	lines := []string{
		"",
		"=========================================================================",
		"[FATAL ERROR] Insufficient free disk space for a safe upgrade.",
		"A full filesystem during dpkg unpack leaves packages half-configured,",
		"which can be very hard to recover without offline rescue access.",
		"",
	}
	for path, free := range low {
		lines = append(lines, fmt.Sprintf("  %s: %s free (need at least %s)", path, humanBytes(free), humanBytes(minFreeBytes)))
	}

	lines = append(lines,
		"",
		"HOW TO FIX:",
		"1. Free space under /var/cache/apt via: apt-get clean",
		"2. Remove unused kernels: apt-get autoremove --purge",
		"3. Inspect large files: du -xhd 1 / | sort -h | tail",
		"=========================================================================",
		"",
	)
	app.printLines(lines...)

	if !app.cfg.DryRun {
		app.failOnError(errors.New("insufficient free disk space"), "Refusing to start upgrade")
	}

	slog.Warn("Would have halted upgrade due to insufficient disk space", "step", "preflight.disk_space", "dry_run", true)
}

func humanBytes(n uint64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}

	div, exp := uint64(unit), 0

	for x := n / unit; x >= unit; x /= unit {
		div *= unit
		exp++
	}

	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}
