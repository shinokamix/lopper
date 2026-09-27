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

Dependencies only point downward: `cli`/`tui` → `engine` → stages → `gitx` → `lopper`,
enforced by [`internal/archtest`](internal/archtest/arch_test.go).

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) for setup, tests and PR conventions.
Security issues: [SECURITY.md](SECURITY.md).

## License

MIT
