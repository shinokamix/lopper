# lopper

Find and safely remove stale git worktrees across your whole machine.

AI coding agents and `git worktree add` leave checkouts scattered over your
disk. `lopper` finds them, explains which ones are safe to delete — and why —
and removes them without losing work.

> Status: early development.

## Usage

```sh
lopper              # interactive TUI, scans your home directory
lopper ~/code       # scan specific paths
lopper scan --json  # machine-readable output for scripts
```

## Architecture

```
cmd/lopper        entry point
internal/
  lopper          core types: Worktree, Facts, Verdict (no dependencies)
  gitx            thin wrapper over the git CLI
  discovery       where worktrees are
  inspect         facts about a worktree (dirty, unpushed, merged, size…)
  verdict         pure rules: facts → safe / review / keep + reasons
  engine          scan pipeline, emits events
  config          user settings
  cli             cobra commands
  tui             Bubble Tea UI: app, list screen, store, theme, keys
```

Dependencies only point downward: `cli`/`tui` → `engine` → stages → `gitx` → `lopper`.
The rules live in [`internal/archtest`](internal/archtest/arch_test.go) and run with the tests:
a new package must be assigned a layer there, and `os/exec`, Charm and cobra are
allowed only in the packages that need them.

## Development

The only requirement is Go. Every other tool (task, golangci-lint, gotestsum,
govulncheck, lefthook) is pinned in [`tools/go.mod`](tools/go.mod) and runs via `go tool`.

```sh
alias task='go tool -modfile=tools/go.mod task'   # or: brew install go-task

task setup    # build tools into .bin/, install git hooks (repo-local)
task          # list all tasks
task check    # what CI runs: tidy, lint (all OSes), tests, govulncheck
task fixture  # sandbox with sample worktrees in .tmp/fixture
task run -- .tmp/fixture
```

Tests:

- unit tests next to the code; `verdict` rules are table-driven;
- end-to-end scenarios in [`cmd/lopper/testdata/script`](cmd/lopper/testdata/script)
  are [testscript](https://pkg.go.dev/github.com/rogpeppe/go-internal/testscript) files
  that build real repositories and run `lopper` against them;
- `task fuzz` fuzzes the `git worktree list` parser.

Commits follow [Conventional Commits](https://www.conventionalcommits.org) (checked by a hook).

## License

MIT
