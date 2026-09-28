# lopper

Find and safely remove stale git worktrees across your whole machine.

AI coding agents and `git worktree add` leave checkouts scattered over your
disk. `lopper` finds them, explains which ones are safe to delete — and why —
and removes them without losing work.

> Status: early development.

## Install

macOS and Linux:

```sh
curl -fsSL https://raw.githubusercontent.com/shinokamix/lopper/main/install.sh | sh
```

This puts `lopper` in `~/.local/bin` after checking it against the release's
checksums; `LOPPER_INSTALL_DIR` picks another directory and `LOPPER_VERSION`
another release. On Windows, download the zip from
[Releases](https://github.com/shinokamix/lopper/releases). With Go:
`go install github.com/shinokamix/lopper/cmd/lopper@latest`.

## Usage

```sh
lopper              # interactive TUI, scans your home and temporary directories
lopper ~/code       # scan specific paths
lopper scan --json  # machine-readable output for scripts
lopper rm ../wt     # remove an existing worktree unless it holds work; --force anyway
```

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md). Security issues: [SECURITY.md](SECURITY.md).

## License

MIT
