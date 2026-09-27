# Contributing to lopper

Thanks for helping out! Bug reports, verdict corrections and small fixes are
always welcome as a PR. For a new feature or a change to how lopper decides
what is safe to delete, please [open an issue](https://github.com/shinokamix/lopper/issues/new/choose)
first so we can agree on the behavior before you write the code.

Found a security issue? Don't open an issue or PR — see [SECURITY.md](SECURITY.md).

## Setup

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

`task setup` installs the hooks into this repository only, even if you have a
global `core.hooksPath`. Pre-commit formats and lints staged packages;
pre-push runs `task check`.

## Architecture

Dependencies only point downward: `cli`/`tui` → `engine` → stages → `gitx` → `lopper`.
See the package map in the [README](README.md#architecture).

The rules live in [`internal/archtest`](internal/archtest/arch_test.go) and run
with the tests: a new package must be assigned a layer there, and `os/exec`,
Charm and cobra are allowed only in the packages that need them. If archtest
fails, move the code rather than loosening the rule — or explain in the PR why
the rule is wrong.

## Tests

- Unit tests live next to the code; `verdict` rules are table-driven.
- End-to-end scenarios in [`cmd/lopper/testdata/script`](cmd/lopper/testdata/script)
  are [testscript](https://pkg.go.dev/github.com/rogpeppe/go-internal/testscript)
  files that build real repositories and run `lopper` against them.
- `task fuzz` fuzzes the `git worktree list` parser.

lopper deletes files, so any change to what it considers **safe** needs a test
that fails without the change: a `verdict` table row for the rule, and a
testscript when the behavior depends on real git state. CI runs the tests on
Linux, macOS and Windows.

Guidance on which tests are worth writing is in
[`.agents/skills/meaningful-tests`](.agents/skills/meaningful-tests/SKILL.md) —
written for AI agents, but it applies to humans too.

## Commits and pull requests

PRs are squash-merged, and the **PR title becomes the commit message** on
`main`. It must follow [Conventional Commits](https://www.conventionalcommits.org)
(a CI check enforces this):

```
<type>(<scope>): <summary in lowercase>

feat(tui): add detail pane
fix(discovery): skip unreadable directories
docs: explain verdict reasons
```

- **Types:** `feat`, `fix`, `perf`, `refactor`, `test`, `docs`, `build`, `ci`,
  `chore`, `style`, `revert`. Add `!` for breaking changes (`feat(cli)!: …`).
- **Scopes** are optional and match the package names: `cli`, `config`,
  `discovery`, `engine`, `gitx`, `inspect`, `lopper`, `tui`, `verdict`;
  `deps` is used by Dependabot.
- `feat`, `fix` and `perf` commits appear in the release notes, so write their
  titles for users.

Commits inside your branch are checked by the `commit-msg` hook too, but they
are squashed away, so don't worry about polishing them.

Before opening a PR:

1. `task check` passes.
2. The PR template is filled in — especially the **Test plan**: what you ran by
   hand and what you saw.
3. Keep PRs focused; unrelated cleanups go in a separate PR.

## AI-assisted contributions

Using AI tools is fine. You are still the author: review every line, run it
yourself, and make sure the PR description reflects what you actually tested.
