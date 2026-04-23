package main

import (
	"context"
	"log/slog"
	"strings"
)

// checkDpkgState refuses to start a new upgrade while dpkg is in an
// unresolved state: half-configured, half-installed, or awaiting triggers.
// A previous run killed mid-transaction leaves these markers, and blindly
// rewriting sources.list on top of that state can brick the system in ways
// the tool cannot recover from automatically.
//
// The fix is always operator-driven: run `dpkg --configure -a`, then re-run
// this tool. The preflight prints that hint but refuses to execute
// `--configure` itself — re-running dpkg mid-problem without human review
// can escalate the failure mode (e.g. if the root cause is a broken
// maintainer script that will fail the same way).
func (app *App) checkDpkgState(ctx context.Context) {
	if app.cfg.DryRun {
		slog.Info("Skipping dpkg --audit (dry-run mode)", "step", "preflight.dpkg_audit", "dry_run", true)

		return
	}

	if app.dpkg == nil {
		slog.Warn("DpkgRunner not configured; skipping dpkg audit", "step", "preflight.dpkg_audit")

		return
	}

	slog.Info("Running pre-flight dpkg --audit...", "step", "preflight.dpkg_audit")

	out, err := app.dpkg.RunWithOutput(ctx, []string{"--audit"})
	if err != nil {
		// dpkg exiting non-zero here is itself a sign the system is sick
		// enough that the operator needs to intervene.
		app.failOnError(err, "dpkg --audit failed; system state cannot be verified")
	}

	findings := nonBlankLines(string(out))
	if len(findings) == 0 {
		slog.Info("dpkg --audit clean.", "step", "preflight.dpkg_audit")

		return
	}

	slog.Error("Preflight failed: dpkg reports unresolved packages",
		"step", "preflight.dpkg_audit",
		"finding_count", len(findings),
		"remediation", "run `dpkg --configure -a` and re-run this tool",
	)
	app.printLines(
		"",
		"=========================================================================",
		"[FATAL ERROR] dpkg reports packages in an unresolved state.",
		"This usually means a previous upgrade was interrupted (Ctrl-C, power",
		"loss, OOM kill). Rewriting sources.list on top of this state can turn",
		"a recoverable hiccup into a bricked system.",
		"",
		"Findings (first 10 lines):",
	)

	limit := min(len(findings), 10)
	for i := range limit {
		app.printLines("  " + findings[i])
	}

	app.printLines(
		"",
		"HOW TO FIX:",
		"1. Run: sudo dpkg --configure -a",
		"2. If that fails, consult the package maintainer's output and resolve",
		"   individual packages manually.",
		"3. Re-run this tool once `dpkg --audit` reports nothing.",
		"=========================================================================",
		"",
	)
	app.failOnError(errDpkgAuditUnclean, "Refusing to start upgrade: dpkg has unresolved packages")
}

// nonBlankLines returns only the trimmed non-empty lines in s.
func nonBlankLines(s string) []string {
	var out []string

	for line := range strings.SplitSeq(s, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}

		out = append(out, trimmed)
	}

	return out
}
