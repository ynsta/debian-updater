package main

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

func (app *App) patchAndTestThirdPartyRepos(finalCodename string) {
	files, err := filepath.Glob("/etc/apt/sources.list.d/*.disabled_by_updater")
	if err != nil || len(files) == 0 {
		return
	}

	for _, disabledFile := range files {
		err = app.patchAndTestThirdPartyRepo(disabledFile, finalCodename)
		if err != nil {
			slog.Error("Failed to patch or test repository", "file", disabledFile, "error", err.Error())
		}
	}
}

func (app *App) patchAndTestThirdPartyRepo(disabledFile, finalCodename string) error {
	originalName, err := repoOriginalName(disabledFile)
	if err != nil {
		return err
	}

	if app.dryRun {
		slog.Info("[DRY RUN] Would patch, re-enable, and test repo", "file", originalName)

		return nil
	}

	content, err := os.ReadFile(disabledFile) // #nosec G304 -- paths are selected from a trusted glob.
	if err != nil {
		return fmt.Errorf("read disabled repo file %s: %w", disabledFile, err)
	}

	patchedContent := string(content)

	for _, oldRelease := range fallbackReleases {
		if oldRelease == finalCodename {
			break
		}

		re := regexp.MustCompile(`\b` + regexp.QuoteMeta(oldRelease) + `\b`)
		patchedContent = re.ReplaceAllString(patchedContent, finalCodename)
	}

	// #nosec G306,G304,G703 -- the path is validated by repoOriginalName and APT source files must stay world-readable.
	err = os.WriteFile(originalName, []byte(patchedContent), 0o644)
	if err != nil {
		return fmt.Errorf("write patched repo file %s: %w", originalName, err)
	}

	err = os.Remove(disabledFile)
	if err != nil {
		return fmt.Errorf("remove disabled repo file %s: %w", disabledFile, err)
	}

	slog.Info("Testing repository connection...", "file", originalName)

	err = app.runAptUpdate()
	if err != nil {
		slog.Warn("Repository failed to update! Keeping it disabled.",
			"file", originalName,
			"reason", "404 Not Found, expired GPG key, or SHA-1 rejection")

		renameErr := os.Rename(originalName, disabledFile)
		if renameErr != nil {
			return fmt.Errorf("re-disable repository %s: %w", originalName, renameErr)
		}

		return nil
	}

	slog.Info("Repository updated successfully. Kept enabled.", "file", originalName)

	return nil
}

func repoOriginalName(disabledFile string) (string, error) {
	const repoDir = "/etc/apt/sources.list.d/"

	const suffix = ".disabled_by_updater"

	cleanPath := filepath.Clean(disabledFile)
	if !strings.HasPrefix(cleanPath, repoDir) {
		return "", fmt.Errorf("unexpected repository path: %s", disabledFile)
	}

	if !strings.HasSuffix(cleanPath, suffix) {
		return "", fmt.Errorf("unexpected disabled repo suffix: %s", disabledFile)
	}

	return strings.TrimSuffix(cleanPath, suffix), nil
}

func (app *App) disableThirdPartyRepos() {
	lists, _ := filepath.Glob("/etc/apt/sources.list.d/*.list")
	sources, _ := filepath.Glob("/etc/apt/sources.list.d/*.sources")

	allFiles := make([]string, 0, len(lists)+len(sources))
	allFiles = append(allFiles, lists...)
	allFiles = append(allFiles, sources...)

	if len(allFiles) == 0 {
		return
	}

	slog.Info("Disabling third-party repositories (deb and deb822 formats)")

	for _, file := range allFiles {
		disabledName := file + ".disabled_by_updater"
		if app.dryRun {
			slog.Info("[DRY RUN] Would disable repo", "file", file, "renamed_to", disabledName)

			continue
		}

		err := os.Rename(file, disabledName)
		if err != nil {
			slog.Error("Failed to disable repo", "file", file, "error", err.Error())

			continue
		}

		slog.Info("Disabled repo", "file", file)
	}
}
