# AGENTS.md

Project tier: **C — Shared critical (client SLA)**
Project type: **Go CLI tool**

The following Tier C rules are intentionally **not applicable** to this project:

- **Monitoring** (metrics, alerts, Grafana dashboards, distributed tracing) — short-lived CLI,
  not a long-running service.
- **Authentication / Secrets / Database** — tool runs locally as root, consumes no user
  credentials and talks to no managed database.
- **Intrinsec product inventory** — this repository is a public OSS project on GitHub, not
  an internally managed service.

All other Tier C rules apply.

## Language

All code comments, documentation, commit messages, ADRs, and inline doc strings must be written
in English, regardless of the team's spoken language.
User-facing strings and UI copy are exempt — use the appropriate language for the audience.

## Code Quality

After modifying any Go file, run `golangci-lint run ./...` before marking work complete.
Fix all lint errors and re-run until the linter exits clean.
Do not consider a task done while lint errors remain.
`gofmt` formatting is non-negotiable — zero diff allowed. Run `gofmt -w .` if in doubt.

## Vulnerability Scanning

After modifying `go.mod` or `go.sum`, run `govulncheck ./...` before marking work complete
(use `govulncheck -mod=vendor ./...` if a `vendor/` directory is present).
Fix called vulnerabilities: `go get <module>@<fixed>`, `go mod tidy`, re-vendor if applicable,
then re-run until clean. Imported-only vulnerabilities must be reported to the user.
Do not consider a task done while called vulnerabilities remain.

## Dependency Management

After any change to `go.mod`, run `go mod tidy`, then `go mod vendor` if third-party
dependencies are introduced. If `vendor/` exists, it must be committed to Git — it must
not be gitignored. Use `go build -mod=vendor` in CI when vendored.
Never run `go get` inside a Docker build without updating the vendor directory afterward.

This project currently has zero third-party dependencies (stdlib only). If a dependency
is added, vendor it immediately and keep the vendor directory in sync.

## Testing & Architecture

Follow Red-Green-Refactor: write a failing test before any implementation code.
Use dependency injection via constructors — no package-level globals, no `init()` side effects.
Define small, focused interfaces at the call site. Never inject a concrete type where an
interface suffices. Push all I/O (exec, HTTP, filesystem, apt calls) to the edges; keep
domain logic free of side effects and testable without a real Debian system.

Integration tests that require a real apt stack run against the Vagrant VM
(see `Vagrantfile`), not the developer's host.

## Project Layout

This is a single-binary CLI tool with a small codebase. The current flat layout
(`main.go`, `apt.go`, `preflight.go`, `releases.go`, `repos.go`, `io_helpers.go`) is
acceptable as long as it stays small. If the codebase grows past roughly 2 000 lines
or gains a second binary, migrate to the layered layout:

```
cmd/<binary>/main.go        # entrypoint + dependency wiring
internal/domain/            # entities, value objects, core interfaces
internal/usecase/           # orchestration of upgrade steps
internal/adapter/apt/       # apt / dpkg exec wrappers
internal/adapter/repo/      # sources.list parsing and rewriting
```

`internal/` is a compiler-enforced boundary — packages inside cannot be imported from
outside the module, which keeps domain logic private by construction.

Tests live next to the code (`foo.go` + `foo_test.go`). Cross-package integration
tests go under `test/` at the module root.

## Dependency Injection

Manual dependency injection via explicit constructors in `main.go`. Do not adopt
Google Wire or Uber Dig for a CLI of this size — manual wiring is the right default
and stays readable here.

## Error Handling

CLI error model:
- Unrecoverable errors (missing prerequisite, user refusal, destructive check failed):
  log at ERROR with full context, exit with a non-zero status, and surface a clear
  human-readable message on stderr.
- Transient errors against network resources (apt mirror, HTTP fetch): retry 1–3 times
  with exponential backoff, log each retry at WARN. If retries are exhausted, fail the
  run — do not continue a partial upgrade.
- Structural errors (missing config, unsupported release): fail immediately at startup.
  No retry, no fallback.

Never swallow errors silently. Every error must include enough context for diagnosis
from the run's captured output alone.

## Logging

Use `slog` (stdlib) with a JSON handler when running non-interactively, or a text
handler when attached to a TTY — never `fmt.Println` or `log.Printf` for anything
that is not direct user-facing prompt output.

Every log entry must include: `timestamp`, `level`, `msg`, `service` (the binary name),
`run_id` (generated per invocation), and any domain-relevant identifiers
(`from_release`, `to_release`, `host`, `step`).

Log at WARN for each retry; ERROR for terminal failures with full context
(command executed, duration, retry count, error chain). Logs go to stdout; diagnostic
run artefacts may be written to a user-specified log path via flag.
Never log secret values, tokens, repository credentials, or proxy credentials, even at
DEBUG level.

## SBOM

Generate a Software Bill of Materials for each release in CycloneDX format using `syft`:

```bash
syft . -o cyclonedx-json > sbom.json
```

Attach the SBOM to the release artifact alongside the compiled binary.
Run `grype sbom.json` to detect vulnerabilities in the release artefact before publishing.

## Dependency Upgrade Policy

Configure Renovate on this repository (see `renovate.json`):
- Auto-merge security patches if all CI checks pass.
- Human review required for minor and major version bumps.
- Group updates by category (dev deps, prod deps, build tools).

Cadence:
- Critical CVE (CVSS ≥ 9.0): patch within 48 hours.
- High CVE (CVSS ≥ 7.0): patch within one sprint.
- Minor patches: monthly.
- Minor versions: quarterly with review.
- Major versions: planned, one at a time.

Never let a dependency fall more than 2 minor versions behind.
The Go toolchain itself is considered a dependency — keep it current.
