<div align="center">

# lopper

Find and remove unused Git worktrees.

[![Release](https://img.shields.io/github/v/release/shinokamix/lopper?style=flat-square&color=8250df)](https://github.com/shinokamix/lopper/releases/latest)
[![CI](https://img.shields.io/github/actions/workflow/status/shinokamix/lopper/ci.yml?branch=main&style=flat-square&label=ci)](https://github.com/shinokamix/lopper/actions/workflows/ci.yml)

<img src="docs/demo.gif" alt="lopper removes four merged worktrees and, after a warning, two with uncommitted work" width="800">

</div>

Git worktrees can stay on disk after a task is done, along with their
dependencies and build output.

lopper finds worktrees across repositories and shows their status and
disk usage. Choose which ones to remove in the terminal UI. Your branches
stay in the repository.

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
lopper rm ../wt     # remove if safe
lopper update       # update to the latest release
```

In the TUI, `space` selects a worktree, `d` opens the removal confirmation,
`enter` confirms and `q` quits. `lopper rm` requires `--force` for unsafe
worktrees.

## Update checks

lopper checks GitHub for updates in the background.
Set `LOPPER_NO_UPDATE_CHECK=1` to disable the check.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md). Security issues go through
[SECURITY.md](SECURITY.md).

## License

[MIT](LICENSE)
