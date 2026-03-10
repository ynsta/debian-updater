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

Run a dry-run inside the Vagrant VM:

```bash
make vagrant-dry
```

The program also accepts:

```bash
./debian-updater --dry-run
```

## Command Line Options

- `--dry-run`: Simulate the entire upgrade process without making any changes to the system. Highly recommended for first runs.
- `--insecure`: Skip SSL/TLS certificate verification for external API calls (e.g., fetching release history).

### When to use --insecure

Legacy Debian systems (like Jessie or Stretch) often have severely outdated `ca-certificates` packages. This can cause the tool to fail when trying to reach HTTPS APIs because it cannot verify the server's certificate. 

Use `--insecure` if:
1. You see errors like `x509: certificate signed by unknown authority`.
2. The system clock is incorrect (common on older hardware), causing certificate validation to fail.
3. You are running in a restricted environment with a self-signed intercepting proxy.

**Note:** The tool still verifies the integrity of Debian packages using GPG signatures via APT; `--insecure` only affects the initial metadata fetching from external APIs.

## Notes

- root is required for real upgrades
- `--dry-run` is useful for validating the control flow
- `golangci-lint` is expected to be installed locally for `make lint`
