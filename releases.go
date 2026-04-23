package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
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

func (app *App) mustTargetCodename(ctx context.Context) string {
	targetCodename := app.getOnlineTargetCodename(ctx)
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

func (app *App) buildReleaseList(ctx context.Context) {
	slog.Info("Attempting to fetch Debian release history online...", "step", "releases.fetch")

	resp, err := app.fetch(ctx, "https://endoflife.date/api/debian.json")
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

func (app *App) getOnlineTargetCodename(ctx context.Context) string {
	resp, err := app.fetch(ctx, "https://ftp.debian.org/debian/dists/stable/Release")
	if err != nil {
		// Cleartext HTTP is only acceptable when the user has already opted in
		// to insecure transport. Otherwise a network-path attacker can force a
		// TLS failure and poison the upgrade target via a MITM'd HTTP response.
		if !app.cfg.InsecureTLS {
			app.failOnError(err, "Failed to fetch latest Debian release info over HTTPS (pass --insecure-tls to allow cleartext fallback)")
		}

		slog.Warn("HTTPS fetch failed, falling back to HTTP per --insecure-tls", "error", err.Error())

		resp, err = app.fetch(ctx, "http://ftp.debian.org/debian/dists/stable/Release")
		app.failOnError(err, "Failed to fetch latest Debian release info")
	}

	defer closeWithWarning("Debian Release Info", resp.Body)

	codename := ""

	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		if value, ok := strings.CutPrefix(scanner.Text(), "Codename:"); ok {
			codename = strings.TrimSpace(value)

			break
		}
	}

	app.failOnError(scanner.Err(), "Failed to read Debian release info")

	if codename == "" {
		return ""
	}

	if indexOf(codename, app.debianReleases) == -1 {
		app.failOnError(
			fmt.Errorf("target codename %q not in known release list", codename),
			"Refusing to upgrade to an unrecognised codename",
		)
	}

	return codename
}

func (app *App) getCurrentCodename() string {
	data, err := app.fs.ReadFile("/etc/os-release")
	if err != nil && app.cfg.DryRun {
		slog.Warn("Could not read /etc/os-release; assuming 'buster' for dry-run simulation", "step", "releases.current")

		return "buster"
	}

	app.failOnError(err, "Failed to read /etc/os-release")

	codename, err := parseOSReleaseCodename(bytes.NewReader(data))
	app.failOnError(err, "Failed to parse /etc/os-release")

	return codename
}

// parseOSReleaseCodename extracts VERSION_CODENAME from an os-release-formatted
// stream. Quotes around the value (single or double) are stripped per the
// os-release spec. Returns the empty string if the key is absent.
func parseOSReleaseCodename(r io.Reader) (string, error) {
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		value, ok := strings.CutPrefix(scanner.Text(), "VERSION_CODENAME=")
		if !ok {
			continue
		}

		return strings.Trim(strings.TrimSpace(value), `"'`), nil
	}

	err := scanner.Err()
	if err != nil {
		return "", fmt.Errorf("scan os-release: %w", err)
	}

	return "", nil
}

func indexOf(val string, arr []string) int {
	for i, v := range arr {
		if v == val {
			return i
		}
	}

	return -1
}
