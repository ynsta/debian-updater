package main

import (
	"context"
	iofs "io/fs"
	"net/http"
	"os"
)

// AptRunner executes a single apt-get invocation. It is deliberately thin: the
// caller is responsible for deciding which arguments to pass, whether to retry
// on failure, and how to interpret exit codes. That keeps the real
// implementation a straight exec.Cmd wrapper and lets tests substitute a
// scripted runner with exact call assertions.
type AptRunner interface {
	Run(ctx context.Context, args []string) error
}

// DpkgRunner executes a single dpkg invocation and returns its combined
// stdout+stderr. The tool uses this narrow surface for diagnostic reads —
// `dpkg --audit`, `dpkg -l` — not for state-changing commands. Keep the
// surface thin so fakes are cheap.
type DpkgRunner interface {
	RunWithOutput(ctx context.Context, args []string) ([]byte, error)
}

// DebconfInspector reads debconf selections for a package. It is split from
// DpkgRunner because `debconf-show` is a separate binary with its own
// availability story — some minimal installs lack the `debconf-utils`
// package entirely, and the preflight must tolerate that.
type DebconfInspector interface {
	Show(ctx context.Context, pkg string) ([]byte, error)
}

// Fetcher performs a single HTTP GET against an already-validated URL. The
// caller owns the response body and must Close it. No retry or timeout logic
// is assumed here — those belong to the App layer so tests can exercise them
// independently.
type Fetcher interface {
	Get(ctx context.Context, rawURL string) (*http.Response, error)
}

// LockProber probes advisory locks without holding them, so callers can
// refuse to proceed when another process is mid-transaction. Real
// implementations wrap flock(2); tests substitute a no-op prober.
type LockProber interface {
	Probe(path string) error
}

// FS wraps every filesystem interaction the tool performs. Consolidating the
// operations onto one interface sacrifices the strict "interfaces at the call
// site" guidance from AGENTS.md, but the alternative — a dozen single-method
// interfaces — makes the wiring in NewApp noisy without improving
// substitutability in practice. Every method maps 1:1 to a stdlib call, so
// the surface stays readable.
type FS interface {
	ReadFile(path string) ([]byte, error)
	WriteAtomic(path string, data []byte, perm os.FileMode) error
	Backup(src, suffix string) (string, error)
	Rename(oldpath, newpath string) error
	Remove(path string) error
	Stat(path string) (iofs.FileInfo, error)
	ReadDir(path string) ([]iofs.DirEntry, error)
	Glob(pattern string) ([]string, error)
	AvailableBytes(path string) (uint64, error)
}
