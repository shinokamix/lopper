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

Windows, in PowerShell:

```powershell
irm https://raw.githubusercontent.com/shinokamix/lopper/main/install.ps1 | iex
```

## Usage

```sh
lopper              # interactive TUI, scans your home and temporary directories
lopper ~/code       # scan specific paths
lopper scan --json  # machine-readable output for scripts
lopper rm ../wt     # remove an existing worktree unless it holds work; --force anyway
lopper update       # install the latest release over this one
```

While it runs, `lopper` checks in the background whether a newer release is
out and, if so, offers it before scanning the next time you start it. A release
you put off with `esc` is offered again a day later, and one you skip with `s`
is not offered again. It asks GitHub at most once a day (an hour
after it could not reach it), and `LOPPER_NO_UPDATE_CHECK=1` turns this off.
Nothing else leaves your machine.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md). Security issues: [SECURITY.md](SECURITY.md).

## License

MIT
