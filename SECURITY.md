# Security policy

## Reporting a vulnerability

Please report vulnerabilities privately through
[GitHub Security Advisories](https://github.com/shinokamix/lopper/security/advisories/new),
not as a public issue or pull request.

## Supported versions

lopper is in early development: only the latest release gets fixes.

## What counts as a vulnerability

lopper scans directories you may not control and deletes files, so we treat
these as security issues:

- lopper deletes, modifies or follows symlinks to anything outside the
  worktree it was asked to remove;
- scanning a repository runs code from it — git hooks, `core.fsmonitor`,
  filters or other commands set in an untrusted `.git/config`;
- a crafted repository or path makes lopper report a worktree as safe to
  delete while it has uncommitted or unpushed work.

A worktree getting the wrong verdict under normal conditions (no crafted
input) is a bug, not a vulnerability — please
[open an issue](https://github.com/shinokamix/lopper/issues/new/choose).
