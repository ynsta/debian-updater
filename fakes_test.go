package main

import (
	"context"
	"errors"
	iofs "io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// fakeAptRunner records apt invocations and optionally returns scripted errors
// keyed by the args' first positional command.
type fakeAptRunner struct {
	mu      sync.Mutex
	calls   [][]string
	fail    map[string]error // keyed by the command verb e.g. "update", "full-upgrade"
	onCallN func(int, []string) error
}

func (f *fakeAptRunner) Run(_ context.Context, args []string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.calls = append(f.calls, args)

	if f.onCallN != nil {
		err := f.onCallN(len(f.calls), args)
		if err != nil {
			return err
		}
	}

	// Find the last positional argument that isn't attached to an "-o" flag.
	verb := ""
	for i := 0; i < len(args); i++ {
		if args[i] == "-o" {
			i++ // skip the option value

			continue
		}

		verb = args[i]
	}

	if err, ok := f.fail[verb]; ok {
		return err
	}

	return nil
}

// fetcherAlwaysErr satisfies the Fetcher port when tests never trigger a
// fetch. Any call panics so we notice accidental network-path exercise.
type fetcherAlwaysErr struct{}

func (fetcherAlwaysErr) Get(context.Context, string) (*http.Response, error) {
	return nil, errors.New("fetcher not wired for this test")
}

// fakeDpkg returns scripted combined output and optional error for dpkg
// invocations. Keyed by the first positional argument ("--audit", "-l", ...).
type fakeDpkg struct {
	mu    sync.Mutex
	calls [][]string
	out   map[string][]byte
	errs  map[string]error
}

func (d *fakeDpkg) RunWithOutput(_ context.Context, args []string) ([]byte, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.calls = append(d.calls, args)

	verb := ""
	if len(args) > 0 {
		verb = args[0]
	}

	return d.out[verb], d.errs[verb]
}

// fakeFS is an in-memory filesystem that satisfies the FS interface. It tracks
// write order so tests can assert that backups occur before atomic writes.
type fakeFS struct {
	mu       sync.Mutex
	files    map[string][]byte
	perms    map[string]os.FileMode
	free     map[string]uint64
	renameOk func(oldpath, newpath string) error
	writes   []string // ordered list of operations: "write:<path>", "backup:<src>", "rename:<src>-><dst>", "remove:<path>"
}

func newFakeFS() *fakeFS {
	return &fakeFS{
		files: map[string][]byte{},
		perms: map[string]os.FileMode{},
		free:  map[string]uint64{},
	}
}

func (f *fakeFS) ReadFile(path string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	data, ok := f.files[path]
	if !ok {
		return nil, os.ErrNotExist
	}

	out := make([]byte, len(data))
	copy(out, data)

	return out, nil
}

func (f *fakeFS) WriteAtomic(path string, data []byte, perm os.FileMode) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.files[path] = append([]byte{}, data...)
	f.perms[path] = perm
	f.writes = append(f.writes, "write:"+path)

	return nil
}

func (f *fakeFS) Backup(src, suffix string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	data, ok := f.files[src]
	if !ok {
		return "", nil
	}

	backup := src + suffix
	f.files[backup] = append([]byte{}, data...)
	f.perms[backup] = f.perms[src]
	f.writes = append(f.writes, "backup:"+src)

	return backup, nil
}

func (f *fakeFS) Rename(oldpath, newpath string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.renameOk != nil {
		err := f.renameOk(oldpath, newpath)
		if err != nil {
			return err
		}
	}

	data, ok := f.files[oldpath]
	if !ok {
		return os.ErrNotExist
	}

	f.files[newpath] = data
	f.perms[newpath] = f.perms[oldpath]
	delete(f.files, oldpath)
	delete(f.perms, oldpath)
	f.writes = append(f.writes, "rename:"+oldpath+"->"+newpath)

	return nil
}

func (f *fakeFS) Remove(path string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	if _, ok := f.files[path]; !ok {
		return os.ErrNotExist
	}

	delete(f.files, path)
	delete(f.perms, path)
	f.writes = append(f.writes, "remove:"+path)

	return nil
}

func (f *fakeFS) Stat(path string) (iofs.FileInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	data, ok := f.files[path]
	if !ok {
		return nil, os.ErrNotExist
	}

	return &fakeFileInfo{name: filepath.Base(path), size: int64(len(data)), mode: f.perms[path]}, nil
}

func (f *fakeFS) ReadDir(path string) ([]iofs.DirEntry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	var entries []iofs.DirEntry

	prefix := strings.TrimRight(path, "/") + "/"

	for p := range f.files {
		if !strings.HasPrefix(p, prefix) {
			continue
		}

		rest := strings.TrimPrefix(p, prefix)
		if strings.Contains(rest, "/") {
			continue
		}

		entries = append(entries, &fakeDirEntry{name: rest, mode: f.perms[p]})
	}

	return entries, nil
}

func (f *fakeFS) Glob(pattern string) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	var matches []string

	for path := range f.files {
		ok, err := filepath.Match(pattern, path)
		if err != nil {
			return nil, err
		}

		if ok {
			matches = append(matches, path)
		}
	}

	return matches, nil
}

func (f *fakeFS) AvailableBytes(path string) (uint64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	free, ok := f.free[path]
	if !ok {
		return 1 << 62, nil // plenty by default
	}

	return free, nil
}

type fakeFileInfo struct {
	name string
	size int64
	mode os.FileMode
}

func (f *fakeFileInfo) Name() string       { return f.name }
func (f *fakeFileInfo) Size() int64        { return f.size }
func (f *fakeFileInfo) Mode() os.FileMode  { return f.mode }
func (f *fakeFileInfo) ModTime() time.Time { return time.Time{} }
func (f *fakeFileInfo) IsDir() bool        { return false }
func (f *fakeFileInfo) Sys() any           { return nil }

type fakeDirEntry struct {
	name string
	mode os.FileMode
}

func (d *fakeDirEntry) Name() string               { return d.name }
func (d *fakeDirEntry) IsDir() bool                { return d.mode.IsDir() }
func (d *fakeDirEntry) Type() iofs.FileMode        { return d.mode.Type() }
func (d *fakeDirEntry) Info() (iofs.FileInfo, error) { return &fakeFileInfo{name: d.name, mode: d.mode}, nil }
