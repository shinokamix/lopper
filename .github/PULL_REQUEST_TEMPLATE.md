<!--
Thanks for contributing to lopper! Please read CONTRIBUTING.md first.

Security issue? Don't open a PR — report it privately, see SECURITY.md.

The PR title becomes the commit on main. Write it as a Conventional Commit in
plain language, describing the outcome rather than the implementation:
  fix(discovery): worktrees under a symlinked home are no longer missed
  feat(tui): the detail pane explains why a worktree is kept

One concern per PR: if the description needs the word "also", split it.
-->

Closes #

## Problem

<!--
One or two sentences: what was wrong or missing, and who notices. Write for a
reviewer who hasn't worked in this part of the codebase.
-->

## Change

<!-- How you fixed it, and anything a reviewer would otherwise have to guess: tradeoffs, rejected alternatives. -->

## Test plan

<!--
What did you actually run by hand, and what did you see? Terminal output or a
screenshot/GIF of the TUI is best; show before and after if output changed.

If this changes which worktrees lopper treats as safe to delete, name the
`verdict` test or testscript that covers it.

Don't paste `go test` or `moon check --all` output — CI shows that. If you didn't
exercise the change manually, write "Not tested" and say why.
-->

<!--
If an AI agent wrote a meaningful part of this PR, end the description with one
plain line naming the model and the tool, and nothing else — no banners or links:

Model: Claude Opus 5.5 (Claude Code)
-->
