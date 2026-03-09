package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"strings"
)

// Fallback list in case the API is offline.
var fallbackReleases = []string{
	"jessie",   // Debian 8
	"stretch",  // Debian 9
	"buster",   // Debian 10
	"bullseye", // Debian 11
	"bookworm", // Debian 12
	"trixie",   // Debian 13
	"forky",    // Debian 14
	"duke",     // Debian 15
}

type EOLRelease struct {
	Cycle    string `json:"cycle"`
	Codename string `json:"codename"`
}

func (app *App) mustCurrentCodename() string {
	currentCodename := app.getCurrentCodename()
	if currentCodename == "" {
		app.failOnError(errors.New("codename empty"), "Could not detect current Debian codename")
	}

	return currentCodename
}

func (app *App) mustTargetCodename() string {
	targetCodename := app.getOnlineTargetCodename()
	if targetCodename == "" {
		app.failOnError(errors.New("target empty"), "Could not detect online target codename")
	}

	return targetCodename
}

func (app *App) mustReleaseIndexes(currentCodename, targetCodename string) (int, int) {
	currIdx := indexOf(currentCodename, app.debianReleases)

	targetIdx := indexOf(targetCodename, app.debianReleases)
	if currIdx == -1 || targetIdx == -1 {
		app.failOnError(errors.New("unknown release"), "Current or target release is not in our known list")
	}

	return currIdx, targetIdx
}

func (app *App) buildReleaseList() {
	slog.Info("Attempting to fetch Debian release history online...")

	resp, err := app.getURL("https://endoflife.date/api/debian.json")
	if err != nil {
		slog.Warn("Failed to reach API. Falling back to hardcoded release list.")

		app.debianReleases = fallbackReleases

		return
	}
	defer closeWithWarning("https://endoflife.date/api/debian.json", resp.Body)

	if resp.StatusCode != http.StatusOK {
		slog.Warn("API returned unexpected status. Falling back to hardcoded release list.", "status", resp.StatusCode)

		app.debianReleases = fallbackReleases

		return
	}

	var releases []EOLRelease

	err = json.NewDecoder(resp.Body).Decode(&releases)
	if err != nil {
		slog.Warn("Failed to parse API response. Falling back to hardcoded release list.")

		app.debianReleases = fallbackReleases

		return
	}

	app.debianReleases = app.debianReleases[:0]

	for i := len(releases) - 1; i >= 0; i-- {
		codename := strings.ToLower(releases[i].Codename)
		if codename != "" {
			app.debianReleases = append(app.debianReleases, codename)
		}
	}

	if len(app.debianReleases) < 5 {
		slog.Warn("API returned suspiciously few releases. Falling back to hardcoded list.")

		app.debianReleases = fallbackReleases

		return
	}

	slog.Info("Successfully built dynamic release list", "total_releases_found", len(app.debianReleases))
}

func (app *App) getOnlineTargetCodename() string {
	resp, err := app.getURL("https://ftp.debian.org/debian/dists/stable/Release")
	app.failOnError(err, "Failed to fetch latest Debian release info")

	defer closeWithWarning("https://ftp.debian.org/debian/dists/stable/Release", resp.Body)

	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		if codename, ok := strings.CutPrefix(scanner.Text(), "Codename:"); ok {
			return strings.TrimSpace(codename)
		}
	}

	return ""
}

func (app *App) getCurrentCodename() string {
	file, err := os.Open("/etc/os-release")
	if err != nil && app.dryRun {
		slog.Warn("[DRY RUN] Could not open /etc/os-release. Assuming 'buster' for simulation.")

		return "buster" // Fallback for testing on non-Debian systems like Mac/Windows
	}

	app.failOnError(err, "Failed to open /etc/os-release")

	defer closeWithWarning("/etc/os-release", file)

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		if strings.HasPrefix(scanner.Text(), "VERSION_CODENAME=") {
			return strings.TrimSpace(strings.Split(scanner.Text(), "=")[1])
		}
	}

	return ""
}

func indexOf(val string, arr []string) int {
	for i, v := range arr {
		if v == val {
			return i
		}
	}

	return -1
}
