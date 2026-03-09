# debian-updater

`debian-updater` is a Go command-line tool that automates deep Debian upgrades across multiple releases. It performs pre-flight safety checks, refreshes APT sources for each step, disables third-party repositories during the core upgrade path, and optionally re-tests them afterward.

## What it does

- checks for unsafe disk mount references
- checks initramfs module configuration
- scans APT keyrings for weak SHA-1 signatures
- discovers Debian release history and target stable codename
- upgrades step by step across Debian releases
- disables and re-tests third-party APT repositories

## Usage

Build the binary:

```bash
make build
```

Run the built-in Go test target:

```bash
make test
```

Run lint checks:

```bash
make lint
```

Run a dry-run inside a Debian container:

```bash
make bookworm-dry
```

The program also accepts:

```bash
./debian-updater --dry-run
```

## Notes

- root is required for real upgrades
- `--dry-run` is useful for validating the control flow
- `golangci-lint` is expected to be installed locally for `make lint`
