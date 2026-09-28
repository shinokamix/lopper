package engine

import (
	"context"
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

// A worktree whose directory is gone can only be found through its
// repository, which is how the list shows it; removing it drops git's
// record of it.
func TestRemoveForgetsGoneWorktree(t *testing.T) {
	repo, wt := gitRepo(t)
	if err := os.RemoveAll(wt); err != nil {
		t.Fatal(err)
	}

	gone := lopper.Worktree{ID: lopper.ID(wt), Path: wt, Repo: lopper.Repo{Path: repo}, Prunable: true}
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

	eng := &Engine{Git: lateWrite{Runner: gitx.Exec{}, file: late}}
	if err := eng.Remove(t.Context(), lopper.Worktree{Path: wt}, false); err == nil {
		t.Error("Remove succeeded, want git to refuse the file written late")
	}
	if _, err := os.Stat(late); err != nil {
		t.Errorf("file written late is gone: %v", err)
	}
}
