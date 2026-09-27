---
name: commit
description: Use when writing or rewording a commit message, PR title, or branch name.
---

# Name a change

PRs are squash-merged, so the PR title becomes the commit on `main` and feeds
the release notes. Commit messages inside the branch follow the same rules.

Use Conventional Commits: `<type>(<scope>): <subject>`, where scope is the
affected package.

1. Split changes that do not form one coherent result.
2. Write the subject as a lowercase plain-language description of the outcome:
   what now happens, and where. A reader must understand it without the diff.
   Add a condition when it matters.
3. Do not restate the type or use vague phrases such as `fix bug`,
   `update code`, or `cleanup`.
4. Keep the header under 72 characters. Add a body only for a reason or
   consequence that does not fit in the subject.

Examples:

- `fix(discovery): worktrees under a symlinked home are no longer missed`
- `fix(tui): scan failures are shown and the cursor row stays visible`

Name branches `<type>/<subject>` with a short kebab-case subject that names the
component and the outcome, such as `fix/tui-scan-failures-shown`.
