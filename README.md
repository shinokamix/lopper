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

## What safe means

A worktree is safe to remove when all of these hold:

- Git tracks it at its current path, and it is not locked.
- `git status` shows no changes and no untracked files.
- No file has an index flag that hides its edits from `git status`.
- Its commits are in the base branch, as ancestors or by content after a
  squash, rebase or cherry-pick.

The base is `origin/HEAD`, else local `main`, else `master`. lopper does
not fetch, so it compares with the base as of your last `git fetch`. A
check that could not run counts as failed, never as clean.

A worktree whose folder is already gone is safe too: only Git's record
of it is left, and `git worktree prune` clears it.

## FAQ

### Can it delete unfinished work?

Only when you ask it to. `lopper rm` refuses a worktree that is not
[safe](#what-safe-means) unless you pass `--force`. In the TUI, `d` lists
such worktrees under "removal may lose work", and confirming removes them
anyway. The branch is always kept.

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
