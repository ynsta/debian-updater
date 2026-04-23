package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"
)

const sourcesListPath = "/etc/apt/sources.list"

func (app *App) updateCurrentBaseSystem(ctx context.Context, currentCodename string) {
	slog.Info("Phase 1: updating current base system", "step", "phase1", "codename", currentCodename)
	app.generateCleanSources(currentCodename)
	app.runUpgradeCommands(ctx, currentCodename)
}

func (app *App) upgradeThroughReleases(ctx context.Context, currentCodename string, currIdx, targetIdx int) string {
	for i := currIdx + 1; i <= targetIdx; i++ {
		if ctx.Err() != nil {
			slog.Warn("Upgrade loop aborted by context cancellation", "step", "phase2.abort", "last_release", currentCodename, "cause", context.Cause(ctx))

			return currentCodename
		}

		stepTarget := app.debianReleases[i]
		slog.Info("Phase 2: upgrading OS release", "step", "phase2", "from_release", currentCodename, "to_release", stepTarget)
		app.generateCleanSources(stepTarget)
		app.runUpgradeCommands(ctx, stepTarget)

		currentCodename = stepTarget
		slog.Info("Completed upgrade step", "step", "phase2.done", "now_on", currentCodename)
	}

	return currentCodename
}

// isEOLCodename reports whether a release has been moved to archive.debian.org
// and therefore requires relaxed apt validation flags. Current in-support
// releases (bullseye+) must be upgraded with full signature and validity checks.
func isEOLCodename(codename string) bool {
	switch codename {
	case "jessie", "stretch", "buster":
		return true
	default:
		return false
	}
}

func (app *App) runUpgradeCommands(ctx context.Context, codename string) {
	app.runAptCommandWithRetry(ctx, aptUpdateArgs(codename)...)
	app.runAptCommand(ctx, "upgrade", "-y", "--without-new-pkgs")
	app.runAptCommand(ctx, "full-upgrade", "-y")
	app.runAptCommand(ctx, "autoremove", "-y")
	app.runAptCommand(ctx, "clean")
}

// runAptCommandWithRetry is reserved for idempotent apt operations (update,
// clean): retrying a failed `upgrade` or `full-upgrade` is not safe because
// the transaction may have left dpkg half-configured, and blindly repeating
// the call can compound the damage rather than recover from it.
func (app *App) runAptCommandWithRetry(ctx context.Context, args ...string) {
	err := retry(ctx, retryAttempts, retryBaseDelay, func() error {
		return app.runAptCommandWithError(ctx, args...)
	})
	app.failOnError(err, fmt.Sprintf("Command failed after %d retries: apt-get %v", retryAttempts, args))
}

// aptUpdateArgs returns the apt-get update argument list for a given codename.
// For EOL releases served from archive.debian.org the Release files are expired
// and unsigned, so Check-Valid-Until and authentication enforcement are
// disabled. For every supported release full validation is kept.
func aptUpdateArgs(codename string) []string {
	if isEOLCodename(codename) {
		return []string{
			"-o", "Acquire::Check-Valid-Until=false",
			"-o", "APT::Get::AllowUnauthenticated=true",
			"update",
		}
	}

	return []string{"update"}
}

func (app *App) generateCleanSources(codename string) {
	components := app.chooseComponents(codename)
	content := app.renderSourcesList(codename, components)

	if app.cfg.DryRun {
		slog.Info("Would generate sources.list", "step", "sources.generate", "target", codename, "components", components, "dry_run", true)

		return
	}

	app.ensureAptLocksFree()

	backup, err := app.fs.Backup(sourcesListPath, fmt.Sprintf(".bak-%s-%s", app.runID, time.Now().UTC().Format("20060102T150405Z")))
	app.failOnError(err, "Failed to back up /etc/apt/sources.list")

	if backup != "" {
		slog.Info("Backed up sources.list", "step", "sources.backup", "backup", backup)
	}

	// #nosec G306 -- APT source files must stay world-readable for apt to consume them.
	err = app.fs.WriteAtomic(sourcesListPath, []byte(content), 0o644)
	app.failOnError(err, "Failed to write clean /etc/apt/sources.list")
	slog.Info("Generated clean sources.list", "step", "sources.generate", "target", codename, "components", components)
}

// chooseComponents preserves the components the operator was already using,
// defaulting to a conservative set if the current sources.list cannot be
// parsed. non-free-firmware is required on bookworm+ for most hardware but
// should not be forced on older releases or on operators who deliberately
// stripped contrib/non-free.
func (app *App) chooseComponents(codename string) string {
	preserved := readExistingComponentsFS(app.fs, sourcesListPath)
	if len(preserved) > 0 {
		if app.needsNonFreeFirmware(codename) && !slices.Contains(preserved, "non-free-firmware") {
			preserved = append(preserved, "non-free-firmware")
		}

		return strings.Join(preserved, " ")
	}

	base := "main contrib non-free"
	if app.needsNonFreeFirmware(codename) {
		base += " non-free-firmware"
	}

	return base
}

func (app *App) needsNonFreeFirmware(codename string) bool {
	bookwormIdx := indexOf("bookworm", app.debianReleases)
	if bookwormIdx == -1 {
		return false
	}

	return indexOf(codename, app.debianReleases) >= bookwormIdx
}

// readExistingComponentsFS reads the given sources.list via the FS port and
// returns the component list from the first enabled one-line `deb` entry.
// Splitting this helper out of the App keeps it unit-testable against a
// fake FS.
func readExistingComponentsFS(fsys FS, path string) []string {
	data, err := fsys.ReadFile(path)
	if err != nil {
		return nil
	}

	for line := range strings.SplitSeq(string(data), "\n") {
		components := extractDebLineComponents(line)
		if components != nil {
			return components
		}
	}

	return nil
}

// extractDebLineComponents returns the component tokens of a single enabled
// one-line `deb` entry, or nil if the line is a comment, deb-src, or malformed.
// Format: `deb [opts...] URI suite component [component...]`.
func extractDebLineComponents(line string) []string {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" || strings.HasPrefix(trimmed, "#") {
		return nil
	}

	fields := strings.Fields(trimmed)
	if len(fields) < 4 || fields[0] != "deb" {
		return nil
	}

	start := 1
	if start < len(fields) && strings.HasPrefix(fields[start], "[") {
		// Option blocks may span multiple space-separated tokens, e.g.
		// `[arch=amd64 signed-by=/etc/apt/k.gpg]`. Consume until we see a
		// token that closes the bracket.
		for start < len(fields) {
			closingBracket := strings.HasSuffix(fields[start], "]")
			start++

			if closingBracket {
				break
			}
		}
	}

	// Need at least URI + suite + 1 component after options.
	if len(fields)-start < 3 {
		return nil
	}

	return fields[start+2:]
}

func (app *App) renderSourcesList(codename, components string) string {
	if isEOLCodename(codename) {
		if !app.cfg.TrustEOLArchive {
			app.failOnError(
				errors.New("EOL codename requires --trust-eol-archive"),
				fmt.Sprintf("Refusing to configure archive.debian.org for EOL release %q without --trust-eol-archive; archive mirrors are served without signature expiry/validity enforcement", codename),
			)
		}

		return fmt.Sprintf("deb [trusted=yes] http://archive.debian.org/debian/ %s %s\n", codename, components)
	}

	content := fmt.Sprintf("deb http://deb.debian.org/debian/ %s %s\n", codename, components)
	content += fmt.Sprintf("deb http://security.debian.org/debian-security %s-security %s\n", codename, components)
	content += fmt.Sprintf("deb http://deb.debian.org/debian/ %s-updates %s\n", codename, components)

	return content
}

// runAptUpdate runs `apt-get update` with the flag set appropriate for the
// given codename. It retries transient failures on the assumption that a
// third-party mirror returning a 5xx is often recoverable within a few
// seconds — M6 in the review.
func (app *App) runAptUpdate(ctx context.Context, codename string) error {
	args := aptUpdateArgs(codename)

	return retry(ctx, retryAttempts, retryBaseDelay, func() error {
		return app.runAptCommandWithError(ctx, args...)
	})
}

func (app *App) runAptCommand(ctx context.Context, args ...string) {
	fullArgs := append([]string{
		"-o", "Dpkg::Options::=--force-confdef",
		"-o", "Dpkg::Options::=--force-confold",
	}, args...)
	app.failOnError(app.runAptCommandWithError(ctx, fullArgs...), fmt.Sprintf("Command failed: apt-get %v", args))
}

func (app *App) runAptCommandWithError(ctx context.Context, args ...string) error {
	if app.cfg.DryRun {
		slog.Info("Would execute", "step", "apt.exec", "command", "apt-get", "args", args, "dry_run", true)

		return nil
	}

	slog.Info("Executing apt-get", "step", "apt.exec", "args", args)

	err := app.apt.Run(ctx, args)
	if err != nil {
		return fmt.Errorf("apt runner: %w", err)
	}

	return nil
}
