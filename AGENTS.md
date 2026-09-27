# lopper

Guidance for coding agents. Humans: everything here is also in
[CONTRIBUTING.md](CONTRIBUTING.md), which is the source of truth.

lopper deletes files. A change to what it considers safe to delete needs a test
that fails without the change (see CONTRIBUTING.md → Tests).

## Commits and pull requests

- Never open a PR unless the developer asks for one.
- Title = the squash commit on `main`: a Conventional Commit in plain language
  that describes the outcome, e.g. `fix(discovery): worktrees under a symlinked
  home are no longer missed`. CI checks the format; scopes are package names.
- Body follows `.github/PULL_REQUEST_TEMPLATE.md`: the problem in a sentence or
  two, then the change, then what you actually ran and saw. Don't paste test
  output that CI already shows.
- One concern per PR. If the description says "also", split it.
- End the body with one plain line naming the model and harness, e.g.
  `Model: Claude Opus 5.5 (Claude Code)`. No "Generated with" banners.
