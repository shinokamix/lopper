package engine

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shinokamix/lopper/internal/gitx"
	"github.com/shinokamix/lopper/internal/lopper"
)

// gitRepo creates an isolated repository with one commit on main and a
// linked worktree next to it on branch work, and returns both paths.
func gitRepo(t *testing.T) (repo, wt string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	gitconfig := filepath.Join(dir, ".gitconfig")
	content := "[user]\n\tname = lopper\n\temail = test@lopper.invalid\n[init]\n\tdefaultBranch = main\n"
	if err := os.WriteFile(gitconfig, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_CONFIG_GLOBAL", gitconfig)
	repo, wt = filepath.Join(dir, "repo"), filepath.Join(dir, "wt")
	git(t, dir, "init", "-q", repo)
	git(t, repo, "commit", "-q", "--allow-empty", "-m", "init")
	git(t, repo, "worktree", "add", "-q", "-b", "work", wt)
	return repo, wt
}

// git runs git in dir and fails the test on error.
func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := gitx.Exec{}.Run(t.Context(), dir, args...)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// Only its repository finds a worktree whose directory is gone, and the list
// shows it that way. Removing it drops git's record of it.
func TestRemoveForgetsGoneWorktree(t *testing.T) {
	repo, wt := gitRepo(t)
	if err := os.RemoveAll(wt); err != nil {
		t.Fatal(err)
	}

	gone := lopper.Worktree{ID: lopper.ID(wt), Path: wt, Repo: lopper.Repo{Path: repo}, State: lopper.StateGone}
	if err := New().Remove(t.Context(), gone, false); err != nil {
		t.Fatalf("Remove of a gone worktree: %v", err)
	}
	if list := git(t, repo, "worktree", "list", "--porcelain"); strings.Contains(list, "refs/heads/work") {
		t.Errorf("git still lists the worktree:\n%s", list)
	}
}

// lateWrite is git in a worktree that gets a file just before git removes
// it, as when an agent is still at work there.
type lateWrite struct {
	gitx.Runner
	file string
}

func (l lateWrite) Run(ctx context.Context, dir string, args ...string) (string, error) {
	if len(args) > 1 && args[0] == "worktree" && args[1] == "remove" {
		if err := os.WriteFile(l.file, []byte("late\n"), 0o600); err != nil {
			return "", err
		}
	}
	return l.Runner.Run(ctx, dir, args...)
}

// Unforced, a worktree found safe is removed without --force, so a file
// written after that check is kept by git's own refusal.
func TestRemoveLetsGitGuardLateWork(t *testing.T) {
	_, wt := gitRepo(t)
	late := filepath.Join(wt, "late.txt")

	eng := &Engine{git: lateWrite{Runner: gitx.Exec{}, file: late}}
	if err := eng.Remove(t.Context(), lopper.Worktree{Path: wt}, false); err == nil {
		t.Error("Remove succeeded, want git to refuse the file written late")
	}
	if _, err := os.Stat(late); err != nil {
		t.Errorf("file written late is gone: %v", err)
	}
}

func TestRemoveRechecksIndexFlagsAfterScan(t *testing.T) {
	repo, path := gitRepo(t)
	file := filepath.Join(path, "tracked.txt")
	if err := os.WriteFile(file, []byte("committed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	git(t, path, "add", "tracked.txt")
	git(t, path, "commit", "-q", "-m", "tracked file")
	git(t, repo, "merge", "-q", "work")

	eng := New()
	var wt lopper.Worktree
	checked := false
	for ev := range eng.Scan(t.Context(), Options{Roots: []string{path}}) {
		switch ev := ev.(type) {
		case WorktreeFound:
			wt = ev.Worktree
		case FactsUpdated:
			if ev.Final {
				checked = ev.Safe
			}
		case ScanDone:
			if ev.Err != nil {
				t.Fatal(ev.Err)
			}
		}
	}
	if !checked {
		t.Fatal("clean merged worktree was not scanned as safe")
	}
	git(t, path, "update-index", "--skip-worktree", "tracked.txt")
	if err := os.WriteFile(file, []byte("hidden edit\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := eng.Remove(t.Context(), wt, false)
	if _, ok := errors.AsType[*NotSafeError](err); !ok {
		t.Fatalf("Remove = %v, want refusal after an index flag changed", err)
	}
	if content, err := os.ReadFile(file); err != nil || string(content) != "hidden edit\n" {
		t.Fatalf("content = %q, error = %v, want preserved hidden edit", content, err)
	}
}

// hiddenEdit is git in a worktree where another process, while history is
// read, hides an edit behind assume-unchanged.
type hiddenEdit struct {
	gitx.Runner
	wt, file string
}

func (h hiddenEdit) Run(ctx context.Context, dir string, args ...string) (string, error) {
	if args[0] == "rev-list" {
		if _, err := h.Runner.Run(ctx, h.wt, "update-index", "--assume-unchanged", h.file); err != nil {
			return "", err
		}
		if err := os.WriteFile(filepath.Join(h.wt, h.file), []byte("hidden edit\n"), 0o600); err != nil {
			return "", err
		}
	}
	return h.Runner.Run(ctx, dir, args...)
}

// git's own check before removal misses an edit hidden by index flags, so
// they are read after everything else the inspection asks git.
func TestRemoveChecksIndexFlagsLast(t *testing.T) {
	repo, wt := gitRepo(t)
	file := filepath.Join(wt, "tracked.txt")
	if err := os.WriteFile(file, []byte("committed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	git(t, wt, "add", "tracked.txt")
	git(t, wt, "commit", "-q", "-m", "tracked file")
	git(t, repo, "merge", "-q", "work")

	eng := &Engine{git: hiddenEdit{Runner: gitx.Exec{}, wt: wt, file: "tracked.txt"}}
	err := eng.Remove(t.Context(), lopper.Worktree{Path: wt}, false)
	if _, ok := errors.AsType[*NotSafeError](err); !ok {
		t.Fatalf("Remove = %v, want refusal of the edit hidden during inspection", err)
	}
	if content, err := os.ReadFile(file); err != nil || string(content) != "hidden edit\n" {
		t.Fatalf("content = %q, error = %v, want preserved hidden edit", content, err)
	}
}
