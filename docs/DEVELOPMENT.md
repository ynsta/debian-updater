# Development Guide

## Prerequisites

### Superpowers

Superpowers provides the AI workflow skills used throughout this guide.
Check if it is installed:

```bash
ls ~/.claude/skills/superpowers 2>/dev/null && echo "installed" || echo "not installed"
```

If not installed, follow the instructions at: https://github.com/obra/superpowers

**This is mandatory for this project.** The workflows described below depend on it.

### gh (GitHub CLI)

`gh` is the GitHub CLI used for creating and managing PRs from the terminal.

**Install:**
```bash
# Debian / Ubuntu
sudo apt install gh

# macOS
brew install gh
```

**Authenticate:**
```bash
gh auth login
gh auth status
```

### Go toolchain

```bash
go version               # 1.26+ required (see go.mod)
golangci-lint --version  # install via /lint-go-install if missing
govulncheck -version     # install via /govulncheck-install if missing
```

### Vagrant (integration testing)

Integration tests that need a real Debian system run in a Vagrant VM (`Vagrantfile`).
Install Vagrant + libvirt/virtualbox per your platform.

```bash
vagrant up
vagrant ssh
```

## Local Development

This is a CLI tool — no Docker Compose stack. Build and run locally:

```bash
go build -o debian-updater .
./debian-updater --help
```

For changes that touch apt/dpkg behaviour, iterate inside the Vagrant VM —
never test destructive operations on the developer's host.

## AI-Assisted Workflow

This project uses AI agents with [superpowers](https://agentskills.io) skills.
This is a **critical project with client SLA** — every change follows a strict sequence.
Skipping steps is not allowed.

```
brainstorming → worktree → writing-plans → subagent-driven-development → review → PR
```

### Step by Step

1. **Design** — run `/brainstorming`. The agent asks questions one at a time, proposes 2-3 approaches,
   writes a spec to `docs/superpowers/specs/YYYY-MM-DD-<topic>-design.md`, and waits for your explicit
   approval before proceeding.

2. **Isolate** — the agent creates a git worktree on a feature branch via
   `superpowers:using-git-worktrees`. Never work directly on `main`.

3. **Plan** — run `/writing-plans`. The agent turns the spec into a detailed step-by-step
   implementation plan with test cases and commit checkpoints, saved to
   `docs/superpowers/plans/YYYY-MM-DD-<feature>.md`.

4. **Implement** — the agent executes the plan with `superpowers:subagent-driven-development`:
   one fresh subagent per task, with **two-stage review** after each task:
   - First: spec compliance (does the code match the spec exactly?)
   - Then: code quality (is the implementation well-built?)

5. **Verify** — `superpowers:verification-before-completion` is mandatory before declaring any work done.
   Tests must pass, linter must pass, no called vulnerabilities.

6. **Final review** — `/requesting-code-review` dispatches a dedicated code-reviewer subagent
   across the entire implementation before the PR is opened.

7. **Submit** — `superpowers:finishing-a-development-branch` guides the final step:
   push branch, create GitHub PR, clean up worktree.

## Skill Reference

| Situation | Skill to invoke |
|-----------|----------------|
| Starting a feature or significant change | `/brainstorming` |
| Turning an approved spec into a plan | `/writing-plans` |
| Executing a plan | `superpowers:subagent-driven-development` |
| Bug or unexpected behavior | `/systematic-debugging` |
| Before marking any task complete | `superpowers:verification-before-completion` |
| After all tasks, before opening PR | `/requesting-code-review` |
| Responding to PR feedback | `superpowers:receiving-code-review` |
| After modifying Go files | `/lint-go` |
| After modifying go.mod / go.sum | `/govulncheck` |

## Branch Strategy

```
main  (protected — CI required, no direct push)
 ├── feature/<ticket-id>-<short-description>    new features
 ├── fix/<ticket-id>-<short-description>        bug fixes
 ├── hotfix/<short-description>                 emergency production fixes
 └── chore/<short-description>                  non-functional changes (deps, docs, config)
```

**Rules:**
- **No direct push to `main`** — enforced via GitHub branch protection.
- Branch from latest `main`:
  ```bash
  git fetch origin && git checkout -b feature/add-bookworm-support origin/main
  ```
- Include a ticket or issue ID in the branch name when available.
- Target < 3 days per branch. Long branches mean merge pain and review fatigue.
- One concern per branch — do not bundle unrelated changes.
- Branches are deleted after merge.

## Pull Request Process

```bash
# 1. Push branch
git push -u origin feature/add-bookworm-support

# 2. Open draft PR immediately
gh pr create --draft \
  --title "feat: add bookworm-to-trixie upgrade path" \
  --body "## What
Adds support for upgrading bookworm -> trixie.

## Test plan
- [ ] Unit tests pass
- [ ] Vagrant integration run from bookworm image
- [ ] golangci-lint clean
- [ ] govulncheck clean"

# 3. Before marking ready — all checks must pass locally
golangci-lint run ./...
go test -race ./...
govulncheck ./...          # or govulncheck -mod=vendor ./... if vendor/ exists

# 4. Mark ready
gh pr ready
```

**PR checklist before marking ready:**
- [ ] All tests pass (`go test -race ./...`)
- [ ] Linter clean (`golangci-lint run ./...`)
- [ ] No called vulnerabilities (`govulncheck ./...`)
- [ ] Integration run on Vagrant VM for any apt/dpkg-touching change
- [ ] AGENTS.md rules not violated (no secrets in code, logs structured, etc.)

**Repository settings (configure once in GitHub):**
- Squash merge: required
- Delete head branches: enabled
- Require CI pass before merge: enabled
- Required approvals: 2 (or per project policy)

Address review feedback with `superpowers:receiving-code-review` — verify technical claims
before implementing suggestions.

## Conflict Prevention

- **Communicate before starting**: check issues for who owns which area.
- **Rebase daily on long branches**:
  ```bash
  git fetch origin && git rebase origin/main
  ```
  Prefer rebase over merge to keep history linear and readable.
- **Small PRs**: split large features into sequential PRs with clear dependency order.
  Each PR should be mergeable and leave the tool in a valid state.
- **Feature flags**: for changes that cannot be split, gate behind a CLI flag to merge
  incomplete work safely without exposing it by default.

## Testing Standards

All code follows Red-Green-Refactor (`superpowers:test-driven-development`).
No implementation without a failing test first.

```bash
# Unit tests
go test ./...

# With race detector (mandatory before PR)
go test -race ./...

# Coverage check (minimum 70% domain code, 90% adapters)
go test -cover ./...
```

Integration tests that touch apt / dpkg run in Vagrant:

```bash
vagrant up
vagrant ssh -c 'cd /vagrant && sudo go test ./... -tags=integration'
```

## Release Checklist

Before tagging a release:
- [ ] All CI checks pass
- [ ] `go test -race ./...` clean
- [ ] `govulncheck ./...` clean
- [ ] Release binary built for all target architectures (see `Makefile`)
- [ ] SBOM generated: `syft . -o cyclonedx-json > sbom.json`
- [ ] `grype sbom.json` — no critical or high vulnerabilities
- [ ] CHANGELOG updated with user-visible changes
- [ ] Git tag signed and pushed: `git tag -s vX.Y.Z && git push --tags`
