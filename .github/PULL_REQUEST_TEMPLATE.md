<!--
Thanks for contributing to lopper! Please read CONTRIBUTING.md first.

Security issue? Don't open a PR — report it privately, see SECURITY.md.

The PR title becomes the commit on main, so write it as a Conventional Commit:
  feat(tui): add detail pane
  fix(discovery): skip unreadable directories
-->

Closes #

## Summary

<!--
What does this change and why? Write for a reviewer who hasn't worked in this
part of the codebase.
-->

## Test plan

<!--
What did you actually run by hand, and what did you see? Terminal output or a
screenshot/GIF of the TUI is best; show before and after if output changed.

Don't paste `go test` output — CI shows that. If you didn't exercise the
change manually, write "Not tested" and say why.
-->

## Checklist

- [ ] This changes which worktrees lopper treats as safe to delete — the new
      behavior is covered by a `verdict` test or a testscript in `cmd/lopper/testdata/script`.
      <!-- Leave unchecked and delete this item if it doesn't apply. -->
- [ ] `task check` passes locally.

<!--
If an AI agent wrote a meaningful part of this PR, end the description with one
plain line naming the model and the tool, and nothing else — no banners or links:

Model: Claude Opus 5.5 (Claude Code)
-->
