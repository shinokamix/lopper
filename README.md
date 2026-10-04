# lopper

Clean up the Git worktrees you and your agents left behind.

![lopper removes four merged worktrees and, after a warning, two with uncommitted work](docs/demo.gif)

Worktrees end up all over the disk. You create some by hand, coding
agents create one per task, and every tool keeps them in a different
place. Each one is a copy of the project with its own `node_modules`,
`.venv` or Rust `target/`, and can take up gigabytes.

Git lists worktrees one repository at a time. Nothing shows you all of
them, which repository each one belongs to, or how much space it takes.

lopper finds them and puts them in one list, so you can see what is
there and remove what you no longer need.

## Install

macOS and Linux:

```sh
curl -fsSL https://raw.githubusercontent.com/shinokamix/lopper/main/install.sh | sh
```

Windows, in PowerShell:

```powershell
irm https://raw.githubusercontent.com/shinokamix/lopper/main/install.ps1 | iex
```

With Go:

```sh
go install github.com/shinokamix/lopper/cmd/lopper@latest
```

Binaries are also on the [releases page](https://github.com/shinokamix/lopper/releases/latest).

## Usage

```sh
lopper              # interactive UI, scans home and temporary directories
lopper ~/code       # scan a specific directory
lopper scan --json  # JSON output
lopper rm ../wt     # remove if safe, --force otherwise
lopper update       # update to the latest release
```

lopper checks GitHub for updates in the background.
Set `LOPPER_NO_UPDATE_CHECK=1` to disable the check.

lopper remembers where it found repositories, in `lopper/repos.json`
under your user cache directory, so that the next scan lists their
worktrees first and finds the rest while you work. The cache only
changes how soon worktrees appear, never which, and nothing is removed
without being checked again. Set `LOPPER_NO_CACHE=1` to scan without it.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md). Security issues go through
[SECURITY.md](SECURITY.md).

## License

[MIT](LICENSE)
