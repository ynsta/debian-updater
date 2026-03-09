// Package main upgrades Debian systems across multiple releases.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
)

const logFile = "/var/log/debian_upgrade.log"

type App struct {
	dryRun         bool
	insecure       bool
	outputWriter   io.Writer
	debianReleases []string
}

func main() {
	app := &App{}

	flag.BoolVar(&app.dryRun, "dry-run", false, "Simulate the upgrade process without making system changes")
	flag.BoolVar(&app.insecure, "insecure", false, "Skip SSL certificate verification")
	flag.Parse()

	f, err := os.OpenFile(logFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "Failed to open log file: %v\n", err)

		os.Exit(1)
	}
	defer closeWithWarning(logFile, f)

	app.outputWriter = io.MultiWriter(os.Stdout, f)
	handler := slog.NewTextHandler(app.outputWriter, &slog.HandlerOptions{Level: slog.LevelInfo})
	slog.SetDefault(slog.New(handler))

	app.logStartup()
	app.validateEnvironment()
	app.runPreflightChecks()
	app.buildReleaseList()

	currentCodename := app.mustCurrentCodename()
	targetCodename := app.mustTargetCodename()
	currIdx, targetIdx := app.mustReleaseIndexes(currentCodename, targetCodename)

	app.disableThirdPartyRepos()
	app.updateCurrentBaseSystem(currentCodename)

	if currIdx >= targetIdx {
		slog.Info("System base is fully updated and already on the target release.")
		app.finishAndCleanup(currentCodename)

		return
	}

	currentCodename = app.upgradeThroughReleases(currentCodename, currIdx, targetIdx)
	app.finishAndCleanup(currentCodename)
}

func (app *App) logStartup() {
	if app.dryRun {
		slog.Info("=== DRY-RUN MODE ENABLED: No system changes will be made ===")

		return
	}

	slog.Info("=== Starting Automated Deep-History Debian Upgrade ===")
}

func (app *App) validateEnvironment() {
	if os.Geteuid() != 0 && !app.dryRun {
		app.failOnError(errors.New("not root"), "This script must be run as root (or use --dry-run to test)")
	}
}

func (app *App) runPreflightChecks() {
	app.checkDiskMounts()
	app.checkInitramfsModules()
	app.checkWeakGPGKeys()
}

func (app *App) finishAndCleanup(finalCodename string) {
	slog.Info("All core OS upgrades completed. Evaluating third-party repositories one by one...")
	app.patchAndTestThirdPartyRepos(finalCodename)
	slog.Info("=== Upgrade Process Finished! Please review logs for disabled third-party repos and reboot. ===", "final_codename", finalCodename)
}
