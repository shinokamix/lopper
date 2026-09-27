// Package gitx is a thin wrapper around the git CLI. Shelling out keeps
// worktree semantics, config and safe.directory handling identical to git.
package gitx

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// ErrGitNotFound is returned by Check when no git binary is in PATH.
var ErrGitNotFound = errors.New("git not found in PATH: lopper needs git to inspect worktrees")

// Check verifies that a working git binary is available.
func Check(ctx context.Context) error {
	err := exec.CommandContext(ctx, "git", "version").Run()
	if errors.Is(err, exec.ErrNotFound) {
		return ErrGitNotFound
	}
	if err != nil {
		return fmt.Errorf("git version: %w", err)
	}
	return nil
}

// Runner executes git in a directory. Tests can substitute a fake.
type Runner interface {
	Run(ctx context.Context, dir string, args ...string) (string, error)
}

// Exec runs the git binary found in PATH.
//
// lopper runs git inside arbitrary, possibly untrusted repositories, so
// repository-local config must never make git spawn a program. For the
// commands lopper uses (status, rev-list, worktree, symbolic-ref,
// rev-parse, config) the vectors are:
//   - core.fsmonitor: status runs the configured hook; disabled via -c,
//     which takes precedence over every config file.
//   - filter.<driver>.clean/process: status hashes files whose stat data
//     is stale through the clean filter chosen by .gitattributes or
//     .git/info/attributes. Driver names are arbitrary, so they are looked
//     up first and blanked via GIT_CONFIG_KEY_n (which, unlike -c, accepts
//     any name).
//
// Hooks do not run for these commands, and the pager, editor, ssh and
// credential helpers are never reached. Future commands that print diffs
// must pass --no-ext-diff --no-textconv. Global config is left intact so
// safe.directory keeps working.
type Exec struct{}

func (Exec) Run(ctx context.Context, dir string, args ...string) (string, error) {
	env := append(os.Environ(), "GIT_OPTIONAL_LOCKS=0", "LC_ALL=C")
	if readsFiles(args) {
		drivers, err := run(ctx, dir, env, "config", "-z", "--name-only",
			"--get-regexp", `^filter\..+\.(clean|smudge|process)$`)
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
			err = nil // no filter configured
		}
		if err != nil {
			return "", err
		}
		env = blankFilters(env, drivers)
	}
	return run(ctx, dir, env, args...)
}

// readsFiles reports whether the git command may hash working tree files
// (status, and worktree remove/move, which run status internally).
func readsFiles(args []string) bool {
	return len(args) > 0 && (args[0] == "status" ||
		args[0] == "worktree" && len(args) > 1 && args[1] != "list")
}

func run(ctx context.Context, dir string, env []string, args ...string) (string, error) {
	full := append([]string{"-C", dir, "-c", "core.fsmonitor=false"}, args...)
	cmd := exec.CommandContext(ctx, "git", full...)
	cmd.Env = env
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimRight(stdout.String(), "\n"), nil
}

// blankFilters appends GIT_CONFIG_KEY_n/VALUE_n pairs that set every
// command of the filter drivers named in keys (NUL-separated config keys
// like "filter.x.clean") to the empty string, which disables them. Pairs
// already set by the user's environment are kept.
func blankFilters(env []string, keys string) []string {
	seen := map[string]bool{}
	n, _ := strconv.Atoi(os.Getenv("GIT_CONFIG_COUNT"))
	for key := range strings.SplitSeq(keys, "\x00") {
		i := strings.LastIndexByte(key, '.')
		if i < 0 || seen[key[:i]] {
			continue
		}
		seen[key[:i]] = true
		for _, cmd := range []string{"clean", "smudge", "process"} {
			env = append(env,
				fmt.Sprintf("GIT_CONFIG_KEY_%d=%s.%s", n, key[:i], cmd),
				fmt.Sprintf("GIT_CONFIG_VALUE_%d=", n))
			n++
		}
	}
	if len(seen) == 0 {
		return env
	}
	return append(env, "GIT_CONFIG_COUNT="+strconv.Itoa(n))
}

// WorktreeEntry is one record of `git worktree list --porcelain`.
type WorktreeEntry struct {
	Path     string
	Head     string
	Branch   string // short name, empty when detached
	Bare     bool
	Locked   bool
	Prunable bool
}

// ListWorktrees returns all worktrees of the repository at dir,
// the main worktree first.
func ListWorktrees(ctx context.Context, r Runner, dir string) ([]WorktreeEntry, error) {
	out, err := r.Run(ctx, dir, "worktree", "list", "--porcelain", "-z")
	if err != nil {
		return nil, err
	}
	return parseWorktreeList(out), nil
}

// parseWorktreeList parses `git worktree list --porcelain -z` output:
// NUL-terminated fields, with an empty field ending each record. Fields are
// never split on newlines, so paths and lock reasons may contain them.
func parseWorktreeList(out string) []WorktreeEntry {
	var (
		entries []WorktreeEntry
		cur     *WorktreeEntry
	)
	for field := range strings.SplitSeq(out, "\x00") {
		key, val, _ := strings.Cut(field, " ")
		if key == "worktree" {
			entries = append(entries, WorktreeEntry{Path: val})
			cur = &entries[len(entries)-1]
			continue
		}
		if cur == nil {
			continue // attributes before the first "worktree" field
		}
		switch key {
		case "HEAD":
			cur.Head = val
		case "branch":
			cur.Branch = strings.TrimPrefix(val, "refs/heads/")
		case "bare":
			cur.Bare = true
		case "locked":
			cur.Locked = true
		case "prunable":
			cur.Prunable = true
		}
	}
	return entries
}

// DefaultBranch guesses the base branch: origin/HEAD, then main, then master.
func DefaultBranch(ctx context.Context, r Runner, dir string) string {
	if ref, err := r.Run(ctx, dir, "symbolic-ref", "--short", "refs/remotes/origin/HEAD"); err == nil {
		return ref
	}
	for _, b := range []string{"main", "master"} {
		if _, err := r.Run(ctx, dir, "rev-parse", "--verify", "--quiet", "refs/heads/"+b); err == nil {
			return b
		}
	}
	return ""
}
