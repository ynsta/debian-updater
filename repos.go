package main

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"regexp"
	"strings"
)

func (app *App) patchAndTestThirdPartyRepos(ctx context.Context, finalCodename string) {
	files, err := app.fs.Glob("/etc/apt/sources.list.d/*.disabled_by_updater")
	if err != nil || len(files) == 0 {
		return
	}

	for _, disabledFile := range files {
		if ctx.Err() != nil {
			slog.Warn("Aborting third-party repo restoration due to cancellation", "step", "repos.restore.abort", "cause", context.Cause(ctx))

			return
		}

		err = app.patchAndTestThirdPartyRepo(ctx, disabledFile, finalCodename)
		if err != nil {
			slog.Error("Failed to patch or test repository", "step", "repos.restore", "file", disabledFile, "error", err.Error())
		}
	}
}

func (app *App) patchAndTestThirdPartyRepo(ctx context.Context, disabledFile, finalCodename string) error {
	originalName, err := repoOriginalName(disabledFile)
	if err != nil {
		return err
	}

	if app.cfg.DryRun {
		slog.Info("Would patch, re-enable, and test repo", "step", "repos.restore", "file", originalName, "dry_run", true)

		return nil
	}

	content, err := app.fs.ReadFile(disabledFile)
	if err != nil {
		return fmt.Errorf("read disabled repo file %s: %w", disabledFile, err)
	}

	patchedContent := patchRepoContent(string(content), finalCodename, app.debianReleases)

	// #nosec G306 -- APT source files must stay world-readable.
	err = app.fs.WriteAtomic(originalName, []byte(patchedContent), 0o644)
	if err != nil {
		return fmt.Errorf("write patched repo file %s: %w", originalName, err)
	}

	err = app.fs.Remove(disabledFile)
	if err != nil {
		return fmt.Errorf("remove disabled repo file %s: %w", disabledFile, err)
	}

	slog.Info("Testing repository connection", "step", "repos.verify", "file", originalName)

	err = app.runAptUpdate(ctx, finalCodename)
	if err != nil {
		slog.Warn("Repository failed to update; keeping it disabled",
			"step", "repos.verify",
			"file", originalName,
			"reason", "404 Not Found, expired GPG key, or SHA-1 rejection")

		renameErr := app.fs.Rename(originalName, disabledFile)
		if renameErr != nil {
			return fmt.Errorf("re-disable repository %s: %w", originalName, renameErr)
		}

		return nil
	}

	slog.Info("Repository updated successfully; kept enabled", "step", "repos.verify", "file", originalName)

	return nil
}

func patchRepoContent(content, finalCodename string, releases []string) string {
	patchedContent := content

	for _, oldRelease := range releases {
		if oldRelease == finalCodename {
			break
		}

		re := regexp.MustCompile(`(?i)\b` + regexp.QuoteMeta(oldRelease) + `\b`)
		patchedContent = re.ReplaceAllString(patchedContent, finalCodename)
	}

	return patchedContent
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
	lists, _ := app.fs.Glob("/etc/apt/sources.list.d/*.list")
	sources, _ := app.fs.Glob("/etc/apt/sources.list.d/*.sources")

	allFiles := make([]string, 0, len(lists)+len(sources))
	allFiles = append(allFiles, lists...)
	allFiles = append(allFiles, sources...)

	if len(allFiles) == 0 {
		return
	}

	slog.Info("Disabling third-party repositories", "step", "repos.disable", "count", len(allFiles))

	if app.cfg.DryRun {
		for _, file := range allFiles {
			slog.Info("Would disable repo", "step", "repos.disable", "file", file, "renamed_to", file+".disabled_by_updater", "dry_run", true)
		}

		return
	}

	type renamed struct{ from, to string }

	done := make([]renamed, 0, len(allFiles))

	for _, src := range allFiles {
		dst := src + ".disabled_by_updater"

		err := app.fs.Rename(src, dst)
		if err != nil {
			// Roll back the renames we already completed, so the operator
			// doesn't end up with a half-disabled set of repositories that
			// would be hard to reason about after failure.
			for i := len(done) - 1; i >= 0; i-- {
				undoErr := app.fs.Rename(done[i].to, done[i].from)
				if undoErr != nil {
					slog.Error("Failed to roll back repo disable", "step", "repos.disable.rollback", "file", done[i].from, "error", undoErr.Error())
				}
			}

			app.failOnError(err, "Failed to disable third-party repo; prior renames rolled back")
		}

		done = append(done, renamed{from: src, to: dst})
		slog.Info("Disabled repo", "step", "repos.disable", "file", src)
	}
}
