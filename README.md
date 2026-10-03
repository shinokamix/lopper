<div align="center">

# lopper

Clean up the Git worktrees you and your agents left behind.

<img src="docs/demo.gif" alt="lopper removes four merged worktrees and, after a warning, two with uncommitted work" width="800">

</div>

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

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md). Security issues go through
[SECURITY.md](SECURITY.md).

## License

[MIT](LICENSE)
