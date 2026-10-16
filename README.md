# lopper

Find stale Git worktrees across all your repositories, see their disk
usage and status, and clean up the ones you no longer need.

![lopper removes four merged worktrees and, after a warning, two with uncommitted work](packages/demo/demo.gif)

Worktrees pile up all over the disk. You make some by hand, and coding
agents make one per task, each tool in its own folder. Every worktree
has its own `node_modules`, `.venv` or `target/`, and can take up
gigabytes.

Git lists them one repository at a time, and you have to check each one
yourself before deleting it. lopper finds them all and shows each one's
size and status, so you know what's safe to delete.

## Install

macOS and Linux:

```sh
curl -fsSL https://lopper.dev/install.sh | sh
```

Windows, in PowerShell:

```powershell
irm https://lopper.dev/install.ps1 | iex
```

With Go:

```sh
go install github.com/shinokamix/lopper/apps/lopper/cmd/lopper@latest
```

Binaries are also on the [releases page](https://github.com/shinokamix/lopper/releases/latest).

## Usage

```sh
lopper              # terminal UI, scans home and temporary directories
lopper ~/code       # scan a specific directory
lopper scan --json  # JSON output
lopper rm ../wt     # remove if safe, --force otherwise
lopper update       # update to the latest release
```

lopper remembers where it found repositories, in `lopper/repos.json`
under your user cache directory, so that the next scan lists their
worktrees first and finds the rest while you work. The cache only
changes how soon worktrees appear, never which, and nothing is removed
without being checked again. Set `LOPPER_NO_CACHE=1` to scan without it.

## FAQ

### Can it delete unfinished work?

Not without asking. lopper warns you about uncommitted or unmerged work,
and `lopper rm` needs `--force` for it.

### How does removal work?

lopper checks the worktree again right before removing it, in case it
changed since the scan. Then it runs `git worktree remove`, which
deletes the folder and Git's record of it but keeps the branch. There is
no trash, so the folder is gone for good.

### Where does it look?

Everywhere under your home and temp folders, hidden ones too. lopper
doesn't look for specific tools, so it finds worktrees from Claude Code,
Codex, Cursor, T3 Code, Orca, Paseo or whatever comes next.

### Does it go online?

Only to check GitHub for updates. `LOPPER_NO_UPDATE_CHECK=1` turns that
off.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md). Security issues go through
[SECURITY.md](SECURITY.md).

## License

[MIT](LICENSE)
