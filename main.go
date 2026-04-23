// Package main upgrades Debian systems across multiple releases.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
)

const defaultLogFile = "/var/log/debian_upgrade.log"

type Config struct {
	DryRun          bool
	InsecureTLS     bool
	TrustEOLArchive bool
	LogFile         string
}

type App struct {
	cfg            Config
	apt            AptRunner
	dpkg           DpkgRunner
	fetcher        Fetcher
	fs             FS
	lockProber     LockProber
	outputWriter   io.Writer
	logger         *slog.Logger
	logFile        *os.File
	runID          string
	hostname       string
	debianReleases []string
}

// NewApp wires a fully configured App from a Config and the ports
// (AptRunner, DpkgRunner, Fetcher, FS, LockProber). Tests substitute fake
// implementations; the production main() uses the exec/net-http/os-backed
// adapters.
func NewApp(cfg Config, apt AptRunner, dpkg DpkgRunner, fetcher Fetcher, fsys FS, locks LockProber, output io.Writer, logFile *os.File, logger *slog.Logger, runID, hostname string) *App {
	return &App{
		cfg:          cfg,
		apt:          apt,
		dpkg:         dpkg,
		fetcher:      fetcher,
		fs:           fsys,
		lockProber:   locks,
		outputWriter: output,
		logger:       logger,
		logFile:      logFile,
		runID:        runID,
		hostname:     hostname,
	}
}

func main() {
	cfg, logPath := parseFlags()

	// #nosec G304 -- logPath is set by the operator via --log-file; the tool runs with the user's authority.
	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "Failed to open log file %q: %v\n", logPath, err)

		os.Exit(1)
	}
	defer closeWithWarning(logPath, f)

	hostname, _ := os.Hostname()
	runID := generateRunID()

	logger := newLogger(os.Stdout, f, runID, hostname)
	slog.SetDefault(logger)

	// apt-get's raw output goes to stderr (so the user sees progress) and to
	// the log file (for post-mortem). Stdout is reserved for structured slog
	// output so it stays parseable when piped.
	output := io.MultiWriter(os.Stderr, f)

	app := NewApp(
		cfg,
		newRealAptRunner(output),
		newRealDpkgRunner(),
		newRealFetcher(cfg.InsecureTLS, httpTimeout),
		newRealFS(),
		newRealLockProber(),
		output,
		f,
		logger,
		runID,
		hostname,
	)

	// First SIGINT/SIGTERM cancels the context; long-running apt/HTTP
	// operations that accept ctx will abort at the next awaitable point. A
	// second signal is handled by signal's default terminator, which lets the
	// operator force-exit if the first signal didn't catch an ongoing dpkg
	// transaction. That second-signal path writes a hint to stderr first.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go func() {
		<-ctx.Done()

		slog.Warn("Interrupt received; finishing current step then aborting. Second signal will terminate immediately; run `dpkg --configure -a` afterwards if a dpkg transaction was in flight.",
			"step", "signal.first", "signal_cause", context.Cause(ctx))
	}()

	app.run(ctx)
}

func parseFlags() (Config, string) {
	var (
		cfg            Config
		legacyInsecure bool
	)

	flag.BoolVar(&cfg.DryRun, "dry-run", false, "Simulate the upgrade process without making system changes")
	flag.BoolVar(&cfg.InsecureTLS, "insecure-tls", false, "Skip TLS certificate verification on release metadata fetches; allow cleartext HTTP fallback")
	flag.BoolVar(&cfg.TrustEOLArchive, "trust-eol-archive", false, "Permit upgrades from archive.debian.org for EOL releases (jessie/stretch/buster); disables GPG verification for those mirrors")
	flag.BoolVar(&legacyInsecure, "insecure", false, "Deprecated. Enables both --insecure-tls and --trust-eol-archive")
	flag.StringVar(&cfg.LogFile, "log-file", defaultLogFile, "Path to the structured JSON log file")
	flag.Parse()

	if legacyInsecure {
		cfg.InsecureTLS = true
		cfg.TrustEOLArchive = true
	}

	return cfg, cfg.LogFile
}

func (app *App) run(ctx context.Context) {
	app.logStartup()
	app.validateEnvironment()
	app.runPreflightChecks(ctx)
	app.buildReleaseList(ctx)

	currentCodename := app.mustCurrentCodename()
	targetCodename := app.mustTargetCodename(ctx)
	currIdx, targetIdx := app.mustReleaseIndexes(currentCodename, targetCodename)

	slog.Info("Upgrade plan resolved", "from_release", currentCodename, "to_release", targetCodename, "steps", targetIdx-currIdx)

	app.ensureAptLocksFree()
	app.disableThirdPartyRepos()
	app.updateCurrentBaseSystem(ctx, currentCodename)

	if currIdx >= targetIdx {
		slog.Info("System base is fully updated and already on the target release.", "from_release", currentCodename, "to_release", targetCodename)
		app.finishAndCleanup(ctx, currentCodename)

		return
	}

	currentCodename = app.upgradeThroughReleases(ctx, currentCodename, currIdx, targetIdx)
	app.finishAndCleanup(ctx, currentCodename)
}

func (app *App) logStartup() {
	if app.cfg.DryRun {
		slog.Info("Starting in dry-run mode: no system changes will be made", "log_file", app.cfg.LogFile)

		return
	}

	slog.Info("Starting automated deep-history Debian upgrade", "log_file", app.cfg.LogFile)
}

func (app *App) validateEnvironment() {
	if os.Geteuid() != 0 && !app.cfg.DryRun {
		app.failOnError(errors.New("not root"), "This tool must be run as root (or use --dry-run to test)")
	}
}

func (app *App) runPreflightChecks(ctx context.Context) {
	app.checkDiskMounts()
	app.checkDiskSpace()
	app.checkInitramfsModules()
	app.checkWeakGPGKeys(ctx)
	app.checkDpkgState(ctx)
}

func (app *App) finishAndCleanup(ctx context.Context, finalCodename string) {
	slog.Info("Core OS upgrades completed. Evaluating third-party repositories one by one.", "from_release", finalCodename)
	app.patchAndTestThirdPartyRepos(ctx, finalCodename)
	slog.Info("Upgrade process finished. Review logs for disabled third-party repos before rebooting.", "final_codename", finalCodename)
}
