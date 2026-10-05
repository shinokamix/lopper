# Contributing to lopper

Bug reports, verdict corrections and small fixes are welcome as a PR. For a new
feature or a change to how lopper decides what is safe to delete,
[open an issue](https://github.com/shinokamix/lopper/issues/new/choose) first.
Security issues go through [SECURITY.md](SECURITY.md), not issues or PRs.

## Setup

You need [proto](https://moonrepo.dev/proto): `proto install` fetches the
[Moon](https://moonrepo.dev/moon) version pinned in [`.prototools`](.prototools),
and Moon runs every task with the Go pinned in
[`.moon/toolchains.yml`](.moon/toolchains.yml), installing it on first use. Go
dev tools are pinned in
[`apps/lopper/tools/go.mod`](apps/lopper/tools/go.mod) and run via `go tool`.

```sh
moon tasks          # list tasks
moon check --all    # what CI runs: tidy, lint (all OSes), tests, govulncheck
moon run fix        # tidy, format and autofix what the hooks reject
moon run fixture    # sandbox in apps/lopper/.tmp/fixture
cd apps/lopper
go run ./cmd/lopper .tmp/fixture
```

The first `moon` command installs a pre-commit hook into this checkout: it
checks the formatting of staged Go files and tidiness of both go.mod files, in
seconds.
CI runs what `moon check --all` does, skipping tasks no changed file affects;
run it before pushing to catch the rest early.

Applications live under `apps/` as separate Moon projects. The Go CLI is
`apps/lopper`, with its own module, tools and build configuration. Moon runs its
tasks from that directory; run Moon commands from the repository root.

Binary releases keep repository tags such as `v0.5.0`. The Go module path is
`github.com/shinokamix/lopper/apps/lopper`, and `go install` finds its versions
by tags such as `apps/lopper/v0.5.0`. The release workflow adds that tag for each
`v*` tag, so push only `v0.5.0`.

To check release artifacts without publishing, run from the repository root:

```sh
goreleaser release --config apps/lopper/.goreleaser.yaml --snapshot --clean
```

GoReleaser builds from `apps/lopper` and writes archives to the root `dist/`.

## Architecture

Paths below are relative to `apps/lopper`.

```
cmd/lopper        entry point
internal/
  lopper          core types: Worktree and its State, Facts (no dependencies)
  gitx            thin wrapper over the git CLI
  discovery       where worktrees are
  inspect         facts about a worktree (dirty, unpushed, merged, size…)
  verdict         what lopper concludes: whether a worktree is safe to delete, and its notes
  engine          scan pipeline, emits events
  update          self-update from GitHub releases
  cli             cobra commands; wires everything together, owns the --json format
  tui             Bubble Tea UI: app, list screen, store, theme, keys
```

Dependencies only point downward: `cli`/`tui` → `engine` → stages → `gitx`, and
the stages and UIs share the types in `lopper`. The UIs reach stages only through
`engine`, except `verdict`, whose notes they show.
[`internal/archtest`](apps/lopper/internal/archtest/arch_test.go) enforces this with the
tests: every package needs a layer there, and `os/exec`, Charm and cobra are
allowed only where needed. If it fails, move the code rather than loosening the rule.

## Tests

- Unit tests live next to the code; `verdict.Safe` is table-driven.
- [`cmd/lopper/testdata/script`](apps/lopper/cmd/lopper/testdata/script) holds end-to-end
  [testscript](https://pkg.go.dev/github.com/rogpeppe/go-internal/testscript)
  scenarios that build real repositories and run `lopper` against them.
- `moon run fuzz` fuzzes the `git worktree list` parser.

lopper deletes files: any change to what it considers **safe** needs a test that
fails without it — a `verdict` row, plus a testscript when real git state matters.
See [meaningful-tests](.agents/skills/meaningful-tests/SKILL.md) for which tests
are worth writing.

## Pull requests

PRs are squash-merged and the **title becomes the commit** on `main`, so it must
be a [Conventional Commit](https://www.conventionalcommits.org) (CI checks it;
the scope, if any, is a package name). Describe the outcome in plain language —
`feat`/`fix`/`perf` titles go straight into the release notes:

```
fix(discovery): worktrees under a symlinked home are no longer missed
```

See [commit](.agents/skills/commit/SKILL.md) for how to word titles, commits and
branch names.

Fill in the PR template, and keep one concern per PR: if the description needs
the word "also", split it.

AI tools are fine, but you are the author: review every line and test it
yourself. If an agent wrote a meaningful part, end the description with one
plain line such as `Model: Claude Opus 5.5 (Claude Code)` instead of the tool's
banner.
