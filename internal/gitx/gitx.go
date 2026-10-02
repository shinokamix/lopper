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
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
)

// ErrGitNotFound is returned by Check when no git binary is in PATH.
var ErrGitNotFound = errors.New("git not found in PATH: lopper needs git to inspect worktrees")

// MinVersion is the oldest git lopper runs with: worktree list -z needs
// 2.36, and older git ignores GIT_CONFIG_COUNT, which [Exec] relies on.
var MinVersion = [2]int{2, 36}

// Check verifies that a git binary of at least MinVersion is available.
func Check(ctx context.Context) error {
	out, err := exec.CommandContext(ctx, "git", "version").Output()
	if errors.Is(err, exec.ErrNotFound) {
		return ErrGitNotFound
	}
	if err != nil {
		return fmt.Errorf("git version: %w", err)
	}
	return checkVersion(strings.TrimSpace(string(out)))
}

// checkVersion checks `git version` output, such as "git version 2.39.5
// (Apple Git-154)" or "git version 2.47.1.windows.1", against MinVersion.
func checkVersion(out string) error {
	need := fmt.Sprintf("lopper needs git %d.%d or newer", MinVersion[0], MinVersion[1])
	var version string
	var major, minor int
	if _, err := fmt.Sscanf(out, "git version %s", &version); err != nil {
		return fmt.Errorf("unrecognized git version %q: %s", out, need)
	}
	if _, err := fmt.Sscanf(version, "%d.%d", &major, &minor); err != nil {
		return fmt.Errorf("unrecognized git version %q: %s", out, need)
	}
	if major < MinVersion[0] || major == MinVersion[0] && minor < MinVersion[1] {
		return fmt.Errorf("git %s is too old: %s", version, need)
	}
	return nil
}

// Runner executes git in a directory. Tests can substitute a fake.
type Runner interface {
	Run(ctx context.Context, dir string, args ...string) (string, error)
	RunRaw(ctx context.Context, dir string, args ...string) (string, error)
}

// Exec runs the git binary found in PATH.
//
// lopper runs git inside arbitrary, possibly untrusted repositories, so
// repository-local config must never make git spawn a program. For the
// commands that touch working tree files (status and worktree), the
// settings are below; `worktree remove` runs status
// in the worktree through a child git, which inherits both settings.
//   - core.fsmonitor: status runs the configured hook; disabled via -c,
//     which takes precedence over every config file.
//   - filter.<driver>.clean/process: status hashes files whose stat data
//     is stale through the clean filter chosen by .gitattributes or
//     .git/info/attributes. Driver names are arbitrary, so they are looked
//     up first, in the worktree a command checks too, and blanked via
//     GIT_CONFIG_KEY_n (which, unlike -c, accepts any name).
//
// Hooks do not run for these commands, and the pager, editor, ssh and
// credential helpers are never reached. Lazy fetching is disabled, and
// all transport protocols are blocked for Git versions that ignore
// GIT_NO_LAZY_FETCH. Commands that print diffs must
// pass --no-ext-diff --no-textconv, and log must pass --no-show-signature.
// Global config is left intact so safe.directory keeps working.
//
// git runs in dir and nowhere else: variables such as GIT_DIR that the
// caller exported (a git hook, `git rebase --exec`) are dropped, see
// [Environ].
type Exec struct{}

func (Exec) Run(ctx context.Context, dir string, args ...string) (string, error) {
	out, err := Exec{}.RunRaw(ctx, dir, args...)
	return strings.TrimRight(out, "\n"), err
}

// RunRaw runs git and preserves stdout bytes, including
// trailing newlines. It applies the same restrictions as Run.
func (Exec) RunRaw(ctx context.Context, dir string, args ...string) (string, error) {
	env := append(Environ(), "GIT_OPTIONAL_LOCKS=0", "GIT_NO_LAZY_FETCH=1", "GIT_ALLOW_PROTOCOL=", "LC_ALL=C")
	var drivers []string
	for _, d := range filterConfigs(dir, args) {
		out, err := run(ctx, d, env, "config", "-z", "--name-only",
			"--get-regexp", `^filter\..+\.(clean|smudge|process)$`)
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
			err = nil // no filter configured
		}
		if err != nil {
			return "", err
		}
		drivers = append(drivers, out)
	}
	if len(drivers) > 0 {
		env = blankFilters(env, strings.Join(drivers, "\x00"))
	}
	return runRaw(ctx, dir, env, args...)
}

// repoVars are the variables `git rev-parse --local-env-vars` lists, which
// tie git to one repository: they would override -C. GIT_CONFIG_PARAMETERS
// and GIT_CONFIG_COUNT are on that list too but carry the user's
// `git -c` config rather than a location, so they are kept, as git itself
// does when it runs a command in a submodule.
var repoVars = map[string]bool{
	"GIT_ALTERNATE_OBJECT_DIRECTORIES": true,
	"GIT_CONFIG":                       true,
	"GIT_OBJECT_DIRECTORY":             true,
	"GIT_DIR":                          true,
	"GIT_WORK_TREE":                    true,
	"GIT_IMPLICIT_WORK_TREE":           true,
	"GIT_GRAFT_FILE":                   true,
	"GIT_INDEX_FILE":                   true,
	"GIT_NO_REPLACE_OBJECTS":           true,
	"GIT_REPLACE_REF_BASE":             true,
	"GIT_PREFIX":                       true,
	"GIT_INTERNAL_SUPER_PREFIX":        true, // older git, such as 2.36
	"GIT_SHALLOW_FILE":                 true,
	"GIT_COMMON_DIR":                   true,
}

// Environ is os.Environ without the variables that tie git to the
// caller's repository.
func Environ() []string {
	// Windows matches variable names regardless of case: git_dir is GIT_DIR.
	return withoutRepoVars(runtime.GOOS == "windows", os.Environ())
}

func withoutRepoVars(caseInsensitive bool, env []string) []string {
	return slices.DeleteFunc(env, func(kv string) bool {
		name, _, _ := strings.Cut(kv, "=")
		if caseInsensitive {
			name = strings.ToUpper(name)
		}
		return repoVars[name]
	})
}

// OwnWorkTree runs git in a git directory, such as a submodule's or a
// --separate-git-dir repository, as if it were its own work tree. Such a
// repository names its checkout in core.worktree, and git refuses to run
// at all once that checkout is gone. Only for commands that do not touch
// the work tree: worktree list, symbolic-ref, rev-parse.
type OwnWorkTree struct{ Runner }

func (o OwnWorkTree) Run(ctx context.Context, dir string, args ...string) (string, error) {
	return o.Runner.Run(ctx, dir, append([]string{"--work-tree=" + dir}, args...)...)
}

func (o OwnWorkTree) RunRaw(ctx context.Context, dir string, args ...string) (string, error) {
	return o.Runner.RunRaw(ctx, dir, append([]string{"--work-tree=" + dir}, args...)...)
}

// filterConfigs returns where to look up the filter drivers a git command
// run in dir may use, or nothing if it hashes no working tree files:
// status, and worktree commands other than list, read the config of dir.
// worktree remove and move also run status in the worktree they are
// given, whose own config counts there too (extensions.worktreeConfig).
func filterConfigs(dir string, args []string) []string {
	for len(args) > 0 && strings.HasPrefix(args[0], "--work-tree=") {
		args = args[1:] // from OwnWorkTree
	}
	switch {
	case len(args) > 0 && args[0] == "status":
		return []string{dir}
	case len(args) > 1 && args[0] == "worktree" && (args[1] == "remove" || args[1] == "move"):
		if wt := worktreeArg(dir, args[2:]); wt != "" {
			return []string{dir, wt}
		}
		return []string{dir}
	case len(args) > 1 && args[0] == "worktree" && args[1] != "list":
		return []string{dir}
	}
	return nil
}

// worktreeArg is the worktree that `git worktree remove` or `move` is
// given, if it exists: their first argument that is not an option.
func worktreeArg(dir string, args []string) string {
	for _, a := range args {
		if strings.HasPrefix(a, "-") {
			continue // --force, their only option
		}
		if !filepath.IsAbs(a) {
			a = filepath.Join(dir, a)
		}
		if info, err := os.Stat(a); err == nil && info.IsDir() {
			return a
		}
		return ""
	}
	return ""
}

func run(ctx context.Context, dir string, env []string, args ...string) (string, error) {
	out, err := runRaw(ctx, dir, env, args...)
	return strings.TrimRight(out, "\n"), err
}

func runRaw(ctx context.Context, dir string, env []string, args ...string) (string, error) {
	full := append([]string{"-C", dir, "-c", "core.fsmonitor=false"}, args...)
	cmd := exec.CommandContext(ctx, "git", full...)
	cmd.Env = env
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return "", &Error{Args: args, Err: err, Stderr: strings.TrimSpace(stderr.String())}
	}
	return stdout.String(), nil
}

// MergeBlobsUnchanged checks whether applying ancestor..other to current leaves
// its contents unchanged. merge-file uses Git's built-in text merge, bypasses
// attribute merge drivers, and with --stdout writes no repository objects.
// It merges temporary files: --object-id needs Git 2.43.
func MergeBlobsUnchanged(ctx context.Context, r Runner, dir, current, ancestor, other string) (bool, error) {
	var blobs [3]string
	for i, id := range []string{current, ancestor, other} {
		blob, err := r.RunRaw(ctx, dir, "cat-file", "blob", id)
		if err != nil {
			return false, err
		}
		if strings.IndexByte(blob, 0) >= 0 {
			return false, nil
		}
		blobs[i] = blob
	}
	tmp, err := os.MkdirTemp("", "lopper-merge-*")
	if err != nil {
		return false, err
	}
	defer os.RemoveAll(tmp)
	paths := []string{filepath.Join(tmp, "current"), filepath.Join(tmp, "ancestor"), filepath.Join(tmp, "other")}
	for i, path := range paths {
		if err := os.WriteFile(path, []byte(blobs[i]), 0o600); err != nil {
			return false, err
		}
	}
	out, err := r.RunRaw(ctx, dir, "merge-file", "--stdout", "--quiet", "--diff3", paths[0], paths[1], paths[2])
	if e, ok := errors.AsType[*exec.ExitError](err); ok && e.ExitCode() > 0 && e.ExitCode() <= 127 {
		return false, nil // merge-file returns the number of conflicts
	}
	if err != nil {
		return false, err
	}
	return out == blobs[0], nil
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

// Error is a git command that failed.
type Error struct {
	Args   []string
	Err    error
	Stderr string
}

func (e *Error) Error() string {
	return fmt.Sprintf("git %s: %v: %s", strings.Join(e.Args, " "), e.Err, e.Stderr)
}

func (e *Error) Unwrap() error { return e.Err }

// Message is what git said went wrong, without the command: its last
// "fatal:" or "error:" line, or else its last line.
func (e *Error) Message() string {
	lines := strings.Split(e.Stderr, "\n")
	for _, l := range slices.Backward(lines) {
		for _, p := range []string{"fatal: ", "error: "} {
			if msg, ok := strings.CutPrefix(l, p); ok {
				return msg
			}
		}
	}
	if l := strings.TrimSpace(lines[len(lines)-1]); l != "" {
		return l
	}
	return e.Err.Error()
}

// brief is a git error told by its message alone: for users, whom the
// command git ran does not help.
type brief struct{ git *Error }

func (b brief) Error() string { return b.git.Message() }
func (b brief) Unwrap() error { return b.git }

// briefly returns err told by git's message alone, if git failed.
func briefly(err error) error {
	if e, ok := errors.AsType[*Error](err); ok {
		return brief{e}
	}
	return err
}

// RemoveWorktree removes the linked worktree at path of the repository
// at repo, or only git's record of it when the directory is gone. Unless
// forced, git refuses when the worktree is locked or has modified or
// untracked files. Ignored files go with it; the branch stays.
func RemoveWorktree(ctx context.Context, r Runner, repo, path string, force bool) error {
	args := []string{"worktree", "remove"}
	if force {
		args = append(args, "--force", "--force") // twice for a locked one
	}
	_, err := r.Run(ctx, repo, append(args, path)...)
	return briefly(err)
}

// RepairWorktree relinks the worktree at path with its repository after
// it was moved there by hand, so that git can remove it.
func RepairWorktree(ctx context.Context, r Runner, path string) error {
	_, err := r.Run(ctx, path, "worktree", "repair")
	return briefly(err)
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
