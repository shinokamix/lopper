package gitx

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestParseWorktreeList(t *testing.T) {
	out := "worktree /repo\x00HEAD aaa\x00branch refs/heads/main\x00\x00" +
		"worktree /repo/.claude/worktrees/x\x00HEAD bbb\x00branch refs/heads/feat/x\x00locked\x00\x00" +
		"worktree /tmp/gone\x00HEAD ccc\x00detached\x00prunable gitdir file points to non-existent location\x00\x00" +
		"worktree /tmp/new\nline\x00HEAD ddd\x00detached\x00locked multi\nline reason\x00\x00" +
		"worktree /tmp/with spaces\x00HEAD eee\x00branch refs/heads/s\x00\x00" +
		"worktree /bare.git\x00bare\x00\x00"

	got := parseWorktreeList(out)
	want := []WorktreeEntry{
		{Path: "/repo", Head: "aaa", Branch: "main"},
		{Path: "/repo/.claude/worktrees/x", Head: "bbb", Branch: "feat/x", Locked: true},
		{Path: "/tmp/gone", Head: "ccc", Prunable: true},
		{Path: "/tmp/new\nline", Head: "ddd", Locked: true},
		{Path: "/tmp/with spaces", Head: "eee", Branch: "s"},
		{Path: "/bare.git", Bare: true},
	}
	if !slices.Equal(got, want) {
		t.Errorf("parseWorktreeList = %+v, want %+v", got, want)
	}
}

func FuzzParseWorktreeList(f *testing.F) {
	f.Add("worktree /repo\x00HEAD aaa\x00branch refs/heads/main\x00\x00")
	f.Add("worktree /a\x00bare\x00\x00worktree /b\x00HEAD b\x00detached\x00locked reason\x00prunable\x00\x00")
	f.Add("worktree /a\nb\x00\x00worktree\x00\x00")
	f.Add("HEAD without worktree\x00\x00")
	f.Fuzz(func(_ *testing.T, out string) {
		// Malformed input must not panic. The fixture test checks parsed values.
		parseWorktreeList(out)
	})
}

func TestCheckVersion(t *testing.T) {
	cases := []struct {
		out string
		ok  bool
	}{
		{"git version 2.36.0", true},
		{"git version 2.39.5 (Apple Git-154)", true},
		{"git version 2.47.1.windows.1", true},
		{"git version 2.45.0.rc1.12.gabcdef", true},
		{"git version 3.0.0", true},
		{"git version 2.35.8", false},
		{"git version 2.34.1", false},
		{"git version 1.99.0", false},
		{"git version", false},
		{"hub version 2.14.2", false},
	}
	for _, tc := range cases {
		if err := checkVersion(tc.out); (err == nil) != tc.ok {
			t.Errorf("checkVersion(%q) = %v, want ok=%v", tc.out, err, tc.ok)
		}
	}
}

func TestCheck(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	if err := Check(t.Context()); err != nil {
		t.Fatalf("Check with git in PATH: %v", err)
	}
	t.Setenv("PATH", "")
	if err := Check(t.Context()); !errors.Is(err, ErrGitNotFound) {
		t.Fatalf("Check with empty PATH = %v, want ErrGitNotFound", err)
	}
}

func TestBlankFiltersKeepsUserOverrides(t *testing.T) {
	t.Setenv("GIT_CONFIG_COUNT", "1")
	got := blankFilters(nil, "filter.a=b.c.clean\x00filter.a=b.c.smudge\x00")
	want := []string{
		"GIT_CONFIG_KEY_1=filter.a=b.c.clean", "GIT_CONFIG_VALUE_1=",
		"GIT_CONFIG_KEY_2=filter.a=b.c.smudge", "GIT_CONFIG_VALUE_2=",
		"GIT_CONFIG_KEY_3=filter.a=b.c.process", "GIT_CONFIG_VALUE_3=",
		"GIT_CONFIG_COUNT=4",
	}
	if !slices.Equal(got, want) {
		t.Errorf("blankFilters = %q, want %q", got, want)
	}
}

// commandRepo creates the repository work/repo with a.txt committed, with git
// isolated from the developer's config. It returns work and a plain git that
// runs there. Commands from config run through sh in the worktree root, so
// ../marker lands in work.
func commandRepo(t *testing.T) (work string, git func(args ...string)) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	work = t.TempDir()
	global := filepath.Join(work, ".gitconfig")
	writeFile(t, global, "[user]\n\tname = lopper\n\temail = test@lopper.invalid\n")
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_CONFIG_GLOBAL", global)

	git = func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", work}, args...)...)
		cmd.Env = Environ()
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	repo := filepath.Join(work, "repo")
	git("init", "-q", repo)
	writeFile(t, filepath.Join(repo, "a.txt"), "hi\n")
	git("-C", repo, "add", "a.txt")
	git("-C", repo, "commit", "-q", "-m", "init")
	return work, git
}

// staleStat dates file at ts, so that git status reads it again and runs
// the commands configured for it.
func staleStat(t *testing.T, file string, ts time.Time) {
	t.Helper()
	if err := os.Chtimes(file, ts, ts); err != nil {
		t.Fatal(err)
	}
}

// TestExecIgnoresRepoCommands checks that repository-local config cannot
// make Exec spawn programs, while plain git does run them.
func TestExecIgnoresRepoCommands(t *testing.T) {
	work, git := commandRepo(t)
	repo, marker := filepath.Join(work, "repo"), filepath.Join(work, "marker")
	writeFile(t, filepath.Join(repo, ".git", "info", "attributes"), "* filter=evil=x.y\n")
	git("-C", repo, "config", "filter.evil=x.y.clean", "touch ../marker; cat")
	git("-C", repo, "config", "core.fsmonitor", "touch ../marker")

	staleStat(t, filepath.Join(repo, "a.txt"), time.Unix(1e9, 0))
	git("-C", repo, "status", "--porcelain")
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("plain git did not run the repo commands, test is ineffective: %v", err)
	}
	if err := os.Remove(marker); err != nil {
		t.Fatal(err)
	}

	staleStat(t, filepath.Join(repo, "a.txt"), time.Unix(2e9, 0))
	if _, err := (Exec{}).Run(t.Context(), repo, "status", "--porcelain"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("Exec ran a command from repository config")
	}
}

// `git worktree remove` checks the worktree with a git status of its own,
// which must not run commands from the repository's config, nor from the
// worktree's own.
func TestRemoveWorktreeIgnoresRepoCommands(t *testing.T) {
	for _, tc := range []struct {
		name   string
		config func(git func(args ...string), repo, wt string)
	}{
		{"repository config", func(git func(args ...string), repo, _ string) {
			git("-C", repo, "config", "filter.evil.clean", "touch ../marker; cat")
		}},
		{"worktree config", func(git func(args ...string), repo, wt string) {
			git("-C", repo, "config", "extensions.worktreeConfig", "true")
			git("-C", wt, "config", "--worktree", "filter.evil.clean", "touch ../marker; cat")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			work, git := commandRepo(t)
			repo, wt, marker := filepath.Join(work, "repo"), filepath.Join(work, "wt"), filepath.Join(work, "marker")
			git("-C", repo, "worktree", "add", "-q", wt)
			writeFile(t, filepath.Join(repo, ".git", "info", "attributes"), "* filter=evil\n")
			tc.config(git, repo, wt)
			// git appends its arguments, which touch would make untracked files.
			git("-C", repo, "config", "core.fsmonitor", "touch ../marker; true")

			staleStat(t, filepath.Join(wt, "a.txt"), time.Unix(1e9, 0))
			git("-C", wt, "status", "--porcelain")
			if _, err := os.Stat(marker); err != nil {
				t.Fatalf("plain git did not run the repo commands, test is ineffective: %v", err)
			}
			if err := os.Remove(marker); err != nil {
				t.Fatal(err)
			}

			staleStat(t, filepath.Join(wt, "a.txt"), time.Unix(2e9, 0))
			if err := RemoveWorktree(t.Context(), Exec{}, repo, wt, false); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(marker); err == nil {
				t.Fatal("git worktree remove ran a command from config")
			}
			if _, err := os.Stat(wt); err == nil {
				t.Fatal("worktree is still there, test is ineffective")
			}
		})
	}
}

// TestExecIgnoresCallerRepo checks that git runs in the directory it is
// given even when the caller points git elsewhere, as git does for hooks.
// Otherwise status would describe the wrong repository.
func TestExecIgnoresCallerRepo(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	work := t.TempDir()
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(work, ".gitconfig"))
	scanned, caller := filepath.Join(work, "scanned"), filepath.Join(work, "caller")
	for _, repo := range []string{scanned, caller} {
		if _, err := (Exec{}).Run(t.Context(), work, "init", "-q", repo); err != nil {
			t.Fatal(err)
		}
	}
	writeFile(t, filepath.Join(scanned, "dirty.txt"), "work\n")

	// Windows ignores the case of variable names, and so does git there.
	dir, workTree, index := "GIT_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE"
	if runtime.GOOS == "windows" {
		dir, workTree, index = "git_dir", "Git_Work_Tree", "git_INDEX_file"
	}
	t.Setenv(dir, filepath.Join(caller, ".git"))
	t.Setenv(workTree, caller)
	t.Setenv(index, filepath.Join(caller, ".git", "index"))
	out, err := (Exec{}).Run(t.Context(), scanned, "status", "--porcelain")
	if err != nil {
		t.Fatal(err)
	}
	if out != "?? dirty.txt" {
		t.Errorf("status in %s = %q, want its untracked dirty.txt", scanned, out)
	}
}

func TestWithoutRepoVars(t *testing.T) {
	env := []string{"PATH=/bin", "GIT_DIR=/a", "git_dir=/b", "Git_Work_Tree=/c", "GIT_CONFIG_COUNT=1", "=C:=C:\\"}
	for _, tc := range []struct {
		caseInsensitive bool
		want            []string
	}{
		// Elsewhere git_dir is a different variable that git never reads.
		{false, []string{"PATH=/bin", "git_dir=/b", "Git_Work_Tree=/c", "GIT_CONFIG_COUNT=1", "=C:=C:\\"}},
		{true, []string{"PATH=/bin", "GIT_CONFIG_COUNT=1", "=C:=C:\\"}},
	} {
		got := withoutRepoVars(tc.caseInsensitive, slices.Clone(env))
		if !slices.Equal(got, tc.want) {
			t.Errorf("withoutRepoVars(caseInsensitive=%v) = %q, want %q", tc.caseInsensitive, got, tc.want)
		}
	}
}

// TestRepoVarsCoverGit fails when the installed git knows a repository
// variable that repoVars misses.
func TestRepoVarsCoverGit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	out, err := exec.Command("git", "rev-parse", "--local-env-vars").Output()
	if err != nil {
		t.Fatal(err)
	}
	for name := range strings.FieldsSeq(string(out)) {
		if !repoVars[name] && name != "GIT_CONFIG_PARAMETERS" && name != "GIT_CONFIG_COUNT" {
			t.Errorf("git reports %s as local to a repository, but Environ keeps it", name)
		}
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// A failed removal tells the user git's reason, not the command it ran.
func TestErrorMessageIsGitsReason(t *testing.T) {
	cases := []struct{ stderr, want string }{
		{"error: failed to delete '/w': Permission denied", "failed to delete '/w': Permission denied"},
		{"hint: use --force\nfatal: '/w' contains modified or untracked files, use --force to delete it\nhint: more", "'/w' contains modified or untracked files, use --force to delete it"},
		{"something unexpected", "something unexpected"},
		{"", "exit status 128"},
	}
	for _, tc := range cases {
		e := &Error{Args: []string{"worktree", "remove", "/w"}, Err: errors.New("exit status 128"), Stderr: tc.stderr}
		if got := briefly(e).Error(); got != tc.want {
			t.Errorf("stderr %q: message %q, want %q", tc.stderr, got, tc.want)
		}
	}
}
