//go:build unix

package main

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	iofs "io/fs"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"
)

// realAptRunner executes apt-get through os/exec. The output streams are
// injected so the command's stdout/stderr can be tee'd to both the log file
// and the operator's terminal.
type realAptRunner struct {
	output io.Writer
}

//nolint:ireturn // DI constructor deliberately returns the AptRunner interface so callers wire adapters through ports.
func newRealAptRunner(output io.Writer) AptRunner {
	return &realAptRunner{output: output}
}

func (r *realAptRunner) Run(ctx context.Context, args []string) error {
	// #nosec G204 -- the binary name is fixed ("apt-get"); only the argument list varies and comes from the tool's internal construction, not untrusted input.
	cmd := exec.CommandContext(ctx, "apt-get", args...)

	cmd.Env = append(os.Environ(),
		"DEBIAN_FRONTEND=noninteractive",
		"DEBCONF_NONINTERACTIVE_SEEN=true",
		"DEBCONF_NOWARNINGS=yes",
		"APT_LISTCHANGES_FRONTEND=none",
	)
	cmd.Stdout = r.output
	cmd.Stderr = r.output

	err := cmd.Run()
	if err != nil {
		return fmt.Errorf("run apt-get %v: %w", args, err)
	}

	return nil
}

// realDpkgRunner executes dpkg commands and returns combined stdout+stderr.
// dpkg prints audit findings to stdout, but warnings occasionally surface on
// stderr — CombinedOutput keeps callers from missing them.
type realDpkgRunner struct{}

//nolint:ireturn // DI constructor returns DpkgRunner interface.
func newRealDpkgRunner() DpkgRunner { return realDpkgRunner{} }

func (realDpkgRunner) RunWithOutput(ctx context.Context, args []string) ([]byte, error) {
	// #nosec G204 -- binary name is fixed ("dpkg"); arguments come from tool-internal preflight logic.
	cmd := exec.CommandContext(ctx, "dpkg", args...)

	out, err := cmd.CombinedOutput()
	if err != nil {
		return out, fmt.Errorf("run dpkg %v: %w", args, err)
	}

	return out, nil
}

// realFetcher is the stdlib-backed Fetcher. It enforces the hostname allowlist
// and injects the operator-controlled InsecureSkipVerify decision. Retry is
// the caller's concern, so each Get is a single attempt.
type realFetcher struct {
	insecureTLS bool
	timeout     time.Duration
}

//nolint:ireturn // DI constructor returns the Fetcher interface so callers can inject fakes.
func newRealFetcher(insecureTLS bool, timeout time.Duration) Fetcher {
	return &realFetcher{insecureTLS: insecureTLS, timeout: timeout}
}

func (f *realFetcher) Get(ctx context.Context, rawURL string) (*http.Response, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("parse URL %q: %w", rawURL, err)
	}

	if _, ok := allowedHosts[parsed.Hostname()]; !ok {
		return nil, fmt.Errorf("refusing to fetch untrusted host %q", parsed.Hostname())
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("build request for %s: %w", rawURL, err)
	}

	tr := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		// #nosec G402 -- InsecureSkipVerify is explicitly controlled via --insecure-tls.
		TLSClientConfig: &tls.Config{InsecureSkipVerify: f.insecureTLS},
	}

	client := &http.Client{
		Timeout:   f.timeout,
		Transport: tr,
	}

	//nolint:gosec,bodyclose // G107/G704: rawURL hostname validated above; response body ownership passes to caller via the App layer's closeWithWarning.
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("send request to %s: %w", rawURL, err)
	}

	return resp, nil
}

// realFS is the stdlib-backed FS. Each method is a direct wrapper so the
// interface imposes no performance or semantic drift. atomicWriteFile /
// backupFile continue to live as package-level helpers and are reused here.
type realFS struct{}

//nolint:ireturn // DI constructor returns the FS interface.
func newRealFS() FS { return &realFS{} }

func (realFS) ReadFile(path string) ([]byte, error) {
	// #nosec G304 -- callers pass well-known system paths; the fs adapter enforces no additional policy.
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}

	return data, nil
}

func (realFS) WriteAtomic(path string, data []byte, perm os.FileMode) error {
	return atomicWriteFile(path, data, perm)
}

func (realFS) Backup(src, suffix string) (string, error) { return backupFile(src, suffix) }

func (realFS) Rename(oldpath, newpath string) error {
	//nolint:gosec // G703: callers are the tool's own logic; no external-path taint.
	err := os.Rename(oldpath, newpath)
	if err != nil {
		return fmt.Errorf("rename %s -> %s: %w", oldpath, newpath, err)
	}

	return nil
}

func (realFS) Remove(path string) error {
	err := os.Remove(path)
	if err != nil {
		return fmt.Errorf("remove %s: %w", path, err)
	}

	return nil
}

func (realFS) Stat(path string) (iofs.FileInfo, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("stat %s: %w", path, err)
	}

	return info, nil
}

func (realFS) ReadDir(path string) ([]iofs.DirEntry, error) {
	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, fmt.Errorf("readdir %s: %w", path, err)
	}

	return entries, nil
}

func (realFS) Glob(pattern string) ([]string, error) {
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return nil, fmt.Errorf("glob %s: %w", pattern, err)
	}

	return matches, nil
}

func (realFS) AvailableBytes(path string) (uint64, error) {
	var stat syscall.Statfs_t

	err := syscall.Statfs(path, &stat)
	if err != nil {
		return 0, fmt.Errorf("statfs %s: %w", path, err)
	}

	//nolint:gosec // G115: Bsize is filesystem block size, always positive.
	return stat.Bavail * uint64(stat.Bsize), nil
}
