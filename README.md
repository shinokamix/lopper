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

lopper is not a worktree manager: it does not create or switch
worktrees. It scans your home and temporary folders, or the folders you
name, and finds orphaned, moved and damaged worktrees too. It sees work
merged by squash, rebase or cherry-pick, and refuses a worktree whose
index flags hide edits from Git.

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
lopper                      # terminal UI, scans home and temporary directories
lopper ~/code               # scan a specific directory
lopper scan --json          # JSON output
lopper scan --safe --paths  # paths of the worktrees safe to delete
lopper rm ../wt             # remove if safe, --force otherwise
lopper rm --all-safe ~/wt   # remove every safe worktree under ~/wt
lopper update               # update to the latest release
```

lopper remembers where it found repositories, in `lopper/repos.json`
under your user cache directory, so that the next scan lists their
worktrees first and finds the rest while you work. The cache only
changes how soon worktrees appear, never which, and nothing is removed
without being checked again. Set `LOPPER_NO_CACHE=1` to scan without it.

Each worktree comes with notes such as "moved by hand" or "not
confirmed by git". [What the notes mean](https://lopper.dev/notes)
says what each one means and how to resolve it, and
[scan --json](https://lopper.dev/scan-json) describes each field
scripts can read.

## Clean up after agents

`lopper rm --all-safe <folder>` removes every safe worktree under the
folder and leaves the rest, so an agent's hook or a scheduler can run
it unattended. It skips the worktree it runs in, so start it from
outside, for example from your home.

## FAQ

### When is a worktree safe?

When Git tracks it where it is, it is not locked, `git status` is clean,
no index flag hides edits, and its commits are in the base branch, even
after a squash or rebase. The base is `origin/HEAD`, `main` or `master`
as of your last fetch. A check that could not run counts as failed.
Ignored files, such as `.env`, are not checked and are deleted with the
worktree.

### Can it delete unfinished work?

Only when you ask it to. `lopper rm` refuses a worktree that is not safe
unless you pass `--force`. In the TUI, `d` lists such worktrees under
"removal may lose work", and confirming removes them anyway. The branch
is always kept.

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
