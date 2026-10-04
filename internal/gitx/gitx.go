// Package gitx wraps the git CLI. Running git itself keeps worktree
// semantics, config and safe.directory handling exactly as git has them.
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

// ErrGitNotFound is returned by Check when PATH has no git binary.
var ErrGitNotFound = errors.New("git not found in PATH: lopper needs git to inspect worktrees")

// minVersion is the oldest git lopper supports. worktree list -z needs
// 2.36, and older git ignores GIT_CONFIG_COUNT, which [Exec] relies on.
var minVersion = [2]int{2, 36}

// Check verifies that PATH has a git of at least minVersion.
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
// (Apple Git-154)" or "git version 2.47.1.windows.1", against minVersion.
func checkVersion(out string) error {
	need := fmt.Sprintf("lopper needs git %d.%d or newer", minVersion[0], minVersion[1])
	var version string
	var major, minor int
	if _, err := fmt.Sscanf(out, "git version %s", &version); err != nil {
		return fmt.Errorf("unrecognized git version %q: %s", out, need)
	}
	if _, err := fmt.Sscanf(version, "%d.%d", &major, &minor); err != nil {
		return fmt.Errorf("unrecognized git version %q: %s", out, need)
	}
	if major < minVersion[0] || major == minVersion[0] && minor < minVersion[1] {
		return fmt.Errorf("git %s is too old: %s", version, need)
	}
	return nil
}

// Runner runs git in a directory. Tests substitute a fake.
type Runner interface {
	Run(ctx context.Context, dir string, args ...string) (string, error)
	RunRaw(ctx context.Context, dir, stdin string, args ...string) (string, error)
}

// Exec runs the git binary found in PATH.
//
// lopper runs git inside untrusted repositories, so repository config must
// never make git start a program. Status and worktree commands read working
// tree files, and Exec disables what they could run:
//   - core.fsmonitor: status runs the configured hook. -c disables it, and
//     -c takes precedence over every config file.
//   - filter.<driver>.clean and process: status passes files with stale stat
//     data through the clean filter that .gitattributes or
//     .git/info/attributes picks. Driver names are arbitrary, so Exec first
//     looks them up, also in the worktree a command checks, then blanks them
//     with GIT_CONFIG_KEY_n, which unlike -c accepts any name.
//
// `worktree remove` runs status in the worktree through a child git, which
// inherits both settings. These commands run no hooks and never reach the
// pager, editor, ssh or credential helpers. GIT_NO_LAZY_FETCH stops lazy
// fetching, and an empty GIT_ALLOW_PROTOCOL blocks every transport for git
// versions that ignore it. Callers that print diffs must pass --no-ext-diff
// --no-textconv, and log must pass --no-show-signature. cat-file without
// --filters runs no programs, and neither does merge-file, which merges
// files outside the repository. Global config stays intact so
// safe.directory keeps working.
//
// git runs only in dir. Exec drops variables such as GIT_DIR that a caller
// like a git hook or `git rebase --exec` exported, see [Environ].
type Exec struct{}

// Run runs git in dir and returns its output without trailing newlines.
func (Exec) Run(ctx context.Context, dir string, args ...string) (string, error) {
	out, err := Exec{}.RunRaw(ctx, dir, "", args...)
	return strings.TrimRight(out, "\n"), err
}

// RunRaw runs git with stdin as input, unless it is empty, and returns
// stdout byte for byte. It applies the same restrictions as Run.
func (Exec) RunRaw(ctx context.Context, dir, stdin string, args ...string) (string, error) {
	env := append(Environ(), "GIT_OPTIONAL_LOCKS=0", "GIT_NO_LAZY_FETCH=1", "GIT_ALLOW_PROTOCOL=", "LC_ALL=C")
	var drivers []string
	for _, d := range filterConfigs(dir, args) {
		out, err := run(ctx, d, env, "", "config", "-z", "--name-only",
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
	return run(ctx, dir, env, stdin, args...)
}

// repoVars are the variables from `git rev-parse --local-env-vars` that tie
// git to one repository and would override -C. That list also has
// GIT_CONFIG_PARAMETERS and GIT_CONFIG_COUNT, but they carry the user's
// `git -c` config, not a location. Environ keeps them, as git does when it
// runs a command in a submodule.
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
	// Windows ignores the case of variable names, so git_dir is GIT_DIR.
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
// --separate-git-dir repository, as its own work tree. Such a repository
// names its checkout in core.worktree, and git refuses to run once that
// checkout is gone. Use it only for commands that leave the work tree
// alone, such as worktree list, symbolic-ref and rev-parse.
type OwnWorkTree struct{ Runner }

// Run runs git in dir with dir as its work tree.
func (o OwnWorkTree) Run(ctx context.Context, dir string, args ...string) (string, error) {
	return o.Runner.Run(ctx, dir, append([]string{"--work-tree=" + dir}, args...)...)
}

// RunRaw runs git in dir with dir as its work tree, as [Exec.RunRaw] does.
func (o OwnWorkTree) RunRaw(ctx context.Context, dir, stdin string, args ...string) (string, error) {
	return o.Runner.RunRaw(ctx, dir, stdin, append([]string{"--work-tree=" + dir}, args...)...)
}

// filterConfigs returns the directories whose config may name filter
// drivers for a git command in dir. Only status and worktree commands other
// than list hash working tree files, and they read the config of dir.
// worktree remove and move also run status in the worktree they are given,
// whose own config applies there with extensions.worktreeConfig.
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

// worktreeArg returns the worktree given to `git worktree remove` or
// `move`, which is their first argument that is not an option, if it
// exists.
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

func run(ctx context.Context, dir string, env []string, stdin string, args ...string) (string, error) {
	full := append([]string{"-C", dir, "-c", "core.fsmonitor=false"}, args...)
	cmd := exec.CommandContext(ctx, "git", full...)
	cmd.Env = env
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return "", &Error{Args: args, Err: err, Stderr: strings.TrimSpace(stderr.String())}
	}
	return stdout.String(), nil
}

// BlobSizes returns the sizes of the blobs ids, using one git process. A
// missing blob, as in a partial clone, is an error.
func BlobSizes(ctx context.Context, r Runner, dir string, ids []string) (map[string]int, error) {
	out, err := r.RunRaw(ctx, dir, strings.Join(ids, "\n")+"\n", "cat-file", "--batch-check")
	if err != nil {
		return nil, err
	}
	sizes := make(map[string]int, len(ids))
	for range ids {
		header, rest, _ := strings.Cut(out, "\n")
		id, size, err := blobHeader(header)
		if err != nil {
			return nil, err
		}
		sizes[id] = size
		out = rest
	}
	return sizes, nil
}

// ReadBlobs returns the contents of the blobs ids, using one git process. A
// missing blob, as in a partial clone, is an error.
func ReadBlobs(ctx context.Context, r Runner, dir string, ids []string) (map[string]string, error) {
	out, err := r.RunRaw(ctx, dir, strings.Join(ids, "\n")+"\n", "cat-file", "--batch")
	if err != nil {
		return nil, err
	}
	blobs := make(map[string]string, len(ids))
	for range ids {
		header, rest, _ := strings.Cut(out, "\n")
		id, size, err := blobHeader(header)
		if err != nil {
			return nil, err
		}
		if size >= len(rest) {
			return nil, fmt.Errorf("git cat-file: truncated output after %s", header)
		}
		blobs[id] = rest[:size]
		out = rest[size+1:] // skip the newline after the contents
	}
	return blobs, nil
}

// blobHeader parses a cat-file header line, "<id> blob <size>".
func blobHeader(header string) (id string, size int, err error) {
	fields := strings.Fields(header)
	if len(fields) != 3 || fields[1] != "blob" {
		return "", 0, fmt.Errorf("git cat-file: %s", header) // "<id> missing"
	}
	size, err = strconv.Atoi(fields[2])
	if err != nil || size < 0 {
		return "", 0, fmt.Errorf("git cat-file: invalid header %q", header)
	}
	return fields[0], size, nil
}

// MergeUnchanged reports whether applying ancestor..other to current, given
// their contents, leaves current unchanged. merge-file uses git's built-in
// text merge, skips attribute merge drivers and, with --stdout, writes no
// objects. It merges temporary files because --object-id needs git 2.43.
func MergeUnchanged(ctx context.Context, r Runner, dir, current, ancestor, other string) (bool, error) {
	blobs := [3]string{current, ancestor, other}
	for _, blob := range blobs {
		if strings.IndexByte(blob, 0) >= 0 {
			return false, nil
		}
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
	out, err := r.RunRaw(ctx, dir, "", "merge-file", "--stdout", "--quiet", "--diff3", paths[0], paths[1], paths[2])
	if e, ok := errors.AsType[*exec.ExitError](err); ok && e.ExitCode() > 0 && e.ExitCode() <= 127 {
		return false, nil // merge-file exits with the number of conflicts
	}
	if err != nil {
		return false, err
	}
	return out == blobs[0], nil
}

// blankFilters appends GIT_CONFIG_KEY_n and GIT_CONFIG_VALUE_n pairs that set
// every command of the drivers in keys to the empty string, which disables
// them. keys are NUL-separated config keys such as filter.x.clean. Pairs the
// user's environment already set stay.
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

// ListWorktrees returns all worktrees of the repository at dir, the main
// worktree first.
func ListWorktrees(ctx context.Context, r Runner, dir string) ([]WorktreeEntry, error) {
	out, err := r.Run(ctx, dir, "worktree", "list", "--porcelain", "-z")
	if err != nil {
		return nil, err
	}
	return parseWorktreeList(out), nil
}

// parseWorktreeList parses `git worktree list --porcelain -z` output. Each
// field ends with NUL and an empty field ends each record, so paths and lock
// reasons may contain newlines.
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

// Message returns git's reason without the command. That is its last
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

// brief shows a git error by its message alone, because the command line
// does not help users.
type brief struct{ git *Error }

func (b brief) Error() string { return b.git.Message() }
func (b brief) Unwrap() error { return b.git }

func briefly(err error) error {
	if e, ok := errors.AsType[*Error](err); ok {
		return brief{e}
	}
	return err
}

// RemoveWorktree removes the linked worktree at path of the repository at
// repo, or only git's record of it when the directory is gone. Unless
// forced, git refuses when the worktree is locked or has modified or
// untracked files. git deletes ignored files with it and keeps the branch.
func RemoveWorktree(ctx context.Context, r Runner, repo, path string, force bool) error {
	args := []string{"worktree", "remove"}
	if force {
		args = append(args, "--force", "--force") // twice for a locked one
	}
	_, err := r.Run(ctx, repo, append(args, path)...)
	return briefly(err)
}

// RepairWorktree relinks a worktree moved to path by hand with its
// repository, so git can remove it.
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
