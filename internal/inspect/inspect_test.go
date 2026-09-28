package inspect

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/shinokamix/lopper/internal/gitx"
	"github.com/shinokamix/lopper/internal/lopper"
)

// setup creates an isolated repository with one commit on main and
// returns its path. Git never reads the developer's system or global config.
func setup(t *testing.T) string {
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

	repo := filepath.Join(dir, "repo")
	git(t, dir, "init", "-q", repo)
	writeFile(t, filepath.Join(repo, "README"), "hello\n")
	git(t, repo, "add", "README")
	git(t, repo, "commit", "-q", "-m", "init")
	return repo
}

// git runs git in dir and fails the test on error.
func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := gitx.Exec{}.Run(context.Background(), dir, args...)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// addWorktree creates the linked worktree "wt" next to repo and describes it the
// way discovery would.
func addWorktree(t *testing.T, repo string, args ...string) lopper.Worktree {
	t.Helper()
	path := filepath.Join(filepath.Dir(repo), "wt")
	git(t, repo, append([]string{"worktree", "add", "-q"}, append(args, path)...)...)
	return lopper.Worktree{
		ID:   lopper.ID(path),
		Path: path,
		Repo: lopper.Repo{Path: repo, DefaultBranch: "main"},
	}
}

func quick(t *testing.T, wt lopper.Worktree) lopper.Facts {
	t.Helper()
	return Inspector{Git: gitx.Exec{}}.Quick(context.Background(), wt)
}

func wantFacts(t *testing.T, f lopper.Facts, dirty, unpushed int, merged lopper.MergeKind) {
	t.Helper()
	if len(f.Errors) > 0 {
		t.Errorf("Errors = %q, want none", f.Errors)
	}
	if f.Dirty == nil || *f.Dirty != dirty {
		t.Errorf("Dirty = %v, want %d", ptr(f.Dirty), dirty)
	}
	if f.Unpushed == nil || *f.Unpushed != unpushed {
		t.Errorf("Unpushed = %v, want %d", ptr(f.Unpushed), unpushed)
	}
	if f.Merged == nil || *f.Merged != merged {
		t.Errorf("Merged = %v, want %s", ptr(f.Merged), merged)
	}
}

func ptr[T any](p *T) any {
	if p == nil {
		return "<nil>"
	}
	return *p
}

func TestQuickCleanMerged(t *testing.T) {
	repo := setup(t)
	wt := addWorktree(t, repo, "-b", "merged")
	wantFacts(t, quick(t, wt), 0, 0, lopper.MergedFF)
}

func TestQuickDirtyUntracked(t *testing.T) {
	repo := setup(t)
	wt := addWorktree(t, repo, "-b", "wip")
	git(t, repo, "config", "status.showUntrackedFiles", "no")
	dir := filepath.Join(wt.Path, "new")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, "a.txt"), "x\n")
	writeFile(t, filepath.Join(dir, "b.txt"), "y\n")
	wantFacts(t, quick(t, wt), 1, 0, lopper.MergedFF)
}

// The repository has no remote, so a new commit exists only here.
func TestQuickUnpushedUnmergedNoRemote(t *testing.T) {
	repo := setup(t)
	wt := addWorktree(t, repo, "-b", "feature")
	git(t, wt.Path, "commit", "-q", "--allow-empty", "-m", "work")
	wantFacts(t, quick(t, wt), 0, 1, lopper.NotMerged)
}

func TestQuickPushedUnmerged(t *testing.T) {
	repo := setup(t)
	origin := filepath.Join(filepath.Dir(repo), "origin.git")
	git(t, repo, "init", "-q", "--bare", origin)
	git(t, repo, "remote", "add", "origin", origin)
	wt := addWorktree(t, repo, "-b", "feature")
	git(t, wt.Path, "commit", "-q", "--allow-empty", "-m", "work")
	git(t, wt.Path, "push", "-q", "origin", "feature")
	wantFacts(t, quick(t, wt), 0, 0, lopper.NotMerged)
}

func TestQuickDetachedWithCommit(t *testing.T) {
	repo := setup(t)
	wt := addWorktree(t, repo, "--detach")
	git(t, wt.Path, "commit", "-q", "--allow-empty", "-m", "orphaned soon")
	wantFacts(t, quick(t, wt), 0, 1, lopper.NotMerged)
}

func TestQuickNoBaseBranch(t *testing.T) {
	repo := setup(t)
	wt := addWorktree(t, repo, "-b", "merged")
	wt.Repo.DefaultBranch = ""
	if f := quick(t, wt); f.Merged != nil {
		t.Errorf("Merged = %v, want unknown", *f.Merged)
	}
}

// A corrupt index makes `git status` fail while history is still readable:
// Dirty must stay unknown and the cause must be recorded.
func TestQuickStatusFails(t *testing.T) {
	repo := setup(t)
	wt := addWorktree(t, repo, "-b", "merged")
	gitdir := git(t, wt.Path, "rev-parse", "--absolute-git-dir")
	writeFile(t, filepath.Join(gitdir, "index"), "garbage")

	f := quick(t, wt)
	if f.Dirty != nil {
		t.Errorf("Dirty = %d, want unknown", *f.Dirty)
	}
	if len(f.Errors) != 1 || !strings.HasPrefix(f.Errors[0], "could not read status: ") {
		t.Fatalf("Errors = %q, want one status error", f.Errors)
	}
	if strings.Contains(f.Errors[0], "\n") {
		t.Errorf("error is not a single line: %q", f.Errors[0])
	}
}

func TestSlowSize(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "nested"), 0o700); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, "a"), "abc")
	writeFile(t, filepath.Join(dir, "nested", "b"), "12345")
	facts := lopper.Facts{Dirty: new(1), Unpushed: new(2), Merged: new(lopper.NotMerged)}
	got := (Inspector{}).Slow(t.Context(), lopper.Worktree{Path: dir}, facts)
	want := lopper.Facts{Dirty: new(1), Unpushed: new(2), Merged: new(lopper.NotMerged), SizeBytes: new(int64(8))}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Slow = %+v, want %+v; size = %v, want 8", got, want, ptr(got.SizeBytes))
	}
}

// A worktree git reports as gone has no directory to measure; it frees
// nothing, which a known zero says and an unknown size would not.
func TestSlowSizeOfGoneWorktreeIsZero(t *testing.T) {
	wt := lopper.Worktree{Path: filepath.Join(t.TempDir(), "gone"), Prunable: true}
	f := (Inspector{}).Slow(t.Context(), wt, lopper.Facts{})
	if f.SizeBytes == nil || *f.SizeBytes != 0 || len(f.Errors) != 0 {
		t.Errorf("size = %v, errors = %q, want 0 and no errors", ptr(f.SizeBytes), f.Errors)
	}
}

func TestSlowSizeUnavailable(t *testing.T) {
	for _, name := range []string{"missing directory", "cancelled"} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if name == "cancelled" {
				writeFile(t, filepath.Join(dir, "a"), "abc")
				cancel()
			} else {
				dir = filepath.Join(dir, "missing")
			}
			f := (Inspector{}).Slow(ctx, lopper.Worktree{Path: dir}, lopper.Facts{})
			if f.SizeBytes != nil {
				t.Errorf("SizeBytes = %d, want unknown", *f.SizeBytes)
			}
			if len(f.Errors) != 1 || !strings.HasPrefix(f.Errors[0], "could not measure size: ") {
				t.Errorf("Errors = %q, want size error", f.Errors)
			}
		})
	}
}

func TestSlowSizeSkipsUnreadableDirectory(t *testing.T) {
	dir := t.TempDir()
	blocked := filepath.Join(dir, "blocked")
	if err := os.Mkdir(blocked, 0o700); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, "a"), "abc")
	writeFile(t, filepath.Join(blocked, "hidden"), "not counted")
	writeFile(t, filepath.Join(dir, "z"), "12345")
	if err := os.Chmod(blocked, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(blocked, 0o700); err != nil {
			t.Error(err)
		}
	})
	if _, err := os.ReadDir(blocked); err == nil {
		t.Skip("directory permissions do not prevent reads on this system")
	} else if !errors.Is(err, fs.ErrPermission) {
		t.Fatal(err)
	}

	f := (Inspector{}).Slow(t.Context(), lopper.Worktree{Path: dir}, lopper.Facts{})
	if f.SizeBytes == nil || *f.SizeBytes != 8 {
		t.Errorf("SizeBytes = %v, want 8 from files before and after unreadable directory", ptr(f.SizeBytes))
	}
	if len(f.Errors) != 0 {
		t.Errorf("Errors = %q, want no scan failure for an unreadable child", f.Errors)
	}

	f = (Inspector{}).Slow(t.Context(), lopper.Worktree{Path: blocked}, lopper.Facts{})
	if f.SizeBytes != nil || len(f.Errors) != 1 {
		t.Errorf("unreadable root: size = %v, errors = %q, want unknown size and one error", ptr(f.SizeBytes), f.Errors)
	}
}
