package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
)

func (app *App) updateCurrentBaseSystem(currentCodename string) {
	slog.Info("=== Phase 1: Updating current base system ===", "codename", currentCodename)
	app.generateCleanSources(currentCodename)
	app.runUpgradeCommands()
}

func (app *App) upgradeThroughReleases(currentCodename string, currIdx, targetIdx int) string {
	for i := currIdx + 1; i <= targetIdx; i++ {
		stepTarget := app.debianReleases[i]
		slog.Info("=== Phase 2: Upgrading OS Release ===", "from", currentCodename, "to", stepTarget)
		app.generateCleanSources(stepTarget)
		app.runUpgradeCommands()

		currentCodename = stepTarget
		slog.Info("=== Successfully completed upgrade step ===", "now_on", currentCodename)
	}

	return currentCodename
}

func (app *App) runUpgradeCommands() {
	app.runAptCommand("-o", "Acquire::Check-Valid-Until=false", "-o", "APT::Get::AllowUnauthenticated=true", "update")
	app.runAptCommand("upgrade", "-y", "--without-new-pkgs")
	app.runAptCommand("full-upgrade", "-y")
	app.runAptCommand("autoremove", "-y")
	app.runAptCommand("clean")
}

func (app *App) generateCleanSources(codename string) {
	components := "main contrib non-free"
	if indexOf(codename, fallbackReleases) >= indexOf("bookworm", fallbackReleases) {
		components = "main contrib non-free non-free-firmware"
	}

	var content string
	if codename == "jessie" || codename == "stretch" || codename == "buster" {
		content = fmt.Sprintf("deb [trusted=yes] http://archive.debian.org/debian/ %s %s\n", codename, components)
	} else {
		content = fmt.Sprintf("deb http://deb.debian.org/debian/ %s %s\n", codename, components)
		content += fmt.Sprintf("deb http://security.debian.org/debian-security %s-security %s\n", codename, components)
		content += fmt.Sprintf("deb http://deb.debian.org/debian/ %s-updates %s\n", codename, components)
	}

	if app.dryRun {
		slog.Info("[DRY RUN] Would generate /etc/apt/sources.list", "target", codename)

		return
	}

	// #nosec G306 -- APT source files are expected to be world-readable.
	err := os.WriteFile("/etc/apt/sources.list", []byte(content), 0o644)
	app.failOnError(err, "Failed to write clean /etc/apt/sources.list")
	slog.Info("Generated clean sources.list", "target", codename)
}

func (app *App) runAptUpdate() error {
	return app.runAptCommandWithError("update")
}

func (app *App) runAptCommand(args ...string) {
	app.failOnError(app.runAptCommandWithError(args...), fmt.Sprintf("Command failed: apt-get %v", args))
}

func (app *App) runAptCommandWithError(args ...string) error {
	if app.dryRun {
		slog.Info("[DRY RUN] Would execute", "command", "apt-get", "args", args)

		return nil
	}

	slog.Info("Executing system command", "command", "apt-get", "args", args)
	// #nosec G204 -- the command name is fixed and only the argument list varies.
	cmd := exec.CommandContext(context.Background(), "apt-get", args...)

	cmd.Env = append(os.Environ(), "DEBIAN_FRONTEND=noninteractive")
	cmd.Stdout = app.outputWriter
	cmd.Stderr = app.outputWriter

	err := cmd.Run()
	if err != nil {
		return fmt.Errorf("run apt-get %v: %w", args, err)
	}

	return nil
}
