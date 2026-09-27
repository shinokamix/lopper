package gitx

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
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
		"worktree /tmp/with spaces\x00HEAD eee\x00branch refs/heads/s\x00\x00"

	got := parseWorktreeList(out)
	if len(got) != 5 {
		t.Fatalf("want 5 entries, got %d: %+v", len(got), got)
	}
	if got[1].Branch != "feat/x" || !got[1].Locked {
		t.Errorf("entry 1 = %+v", got[1])
	}
	if got[2].Branch != "" || !got[2].Prunable {
		t.Errorf("entry 2 = %+v", got[2])
	}
	if got[3].Path != "/tmp/new\nline" || got[3].Head != "ddd" || !got[3].Locked {
		t.Errorf("entry 3 = %+v", got[3])
	}
	if got[4].Path != "/tmp/with spaces" || got[4].Branch != "s" {
		t.Errorf("entry 4 = %+v", got[4])
	}
}

func FuzzParseWorktreeList(f *testing.F) {
	f.Add("worktree /repo\x00HEAD aaa\x00branch refs/heads/main\x00\x00")
	f.Add("worktree /a\x00bare\x00\x00worktree /b\x00HEAD b\x00detached\x00locked reason\x00prunable\x00\x00")
	f.Add("worktree /a\nb\x00\x00worktree\x00\x00")
	f.Add("HEAD without worktree\x00\x00")
	f.Fuzz(func(t *testing.T, out string) {
		var paths []string
		for field := range strings.SplitSeq(out, "\x00") {
			if key, val, _ := strings.Cut(field, " "); key == "worktree" {
				paths = append(paths, val)
			}
		}
		got := parseWorktreeList(out)
		if len(got) != len(paths) {
			t.Fatalf("got %d entries for %d worktree fields", len(got), len(paths))
		}
		for i, e := range got {
			if e.Path != paths[i] {
				t.Errorf("entry %d path = %q, want %q", i, e.Path, paths[i])
			}
		}
	})
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

// TestExecIgnoresRepoCommands checks that repository-local config cannot
// make Exec spawn programs, while plain git does run them.
func TestExecIgnoresRepoCommands(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	work := t.TempDir()
	global := filepath.Join(work, ".gitconfig")
	writeFile(t, global, "[user]\n\tname = lopper\n\temail = test@lopper.invalid\n")
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_CONFIG_GLOBAL", global)

	repo := filepath.Join(work, "repo")
	git := func(args ...string) {
		t.Helper()
		if out, err := exec.Command("git", append([]string{"-C", work}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q", repo)
	writeFile(t, filepath.Join(repo, "a.txt"), "hi\n")
	git("-C", repo, "add", "a.txt")
	git("-C", repo, "commit", "-q", "-m", "init")
	// Commands run through sh in the worktree root, so ../marker is in work.
	writeFile(t, filepath.Join(repo, ".git", "info", "attributes"), "* filter=evil=x.y\n")
	git("-C", repo, "config", "filter.evil=x.y.clean", "touch ../marker; cat")
	git("-C", repo, "config", "core.fsmonitor", "touch ../marker")

	marker := filepath.Join(work, "marker")
	staleStat := func(ts time.Time) {
		t.Helper()
		if err := os.Chtimes(filepath.Join(repo, "a.txt"), ts, ts); err != nil {
			t.Fatal(err)
		}
	}

	staleStat(time.Unix(1e9, 0))
	git("-C", repo, "status", "--porcelain")
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("plain git did not run the repo commands, test is ineffective: %v", err)
	}
	if err := os.Remove(marker); err != nil {
		t.Fatal(err)
	}

	staleStat(time.Unix(2e9, 0))
	if _, err := (Exec{}).Run(context.Background(), repo, "status", "--porcelain"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("Exec ran a command from repository config")
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
