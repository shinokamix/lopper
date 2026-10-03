package inspect

import (
	"context"
	"errors"
	"fmt"
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

// git prepares test repositories, including pushes to local remotes.
func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "git", append([]string{"-C", dir}, args...)...)
	cmd.Env = gitx.Environ()
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimRight(string(out), "\n")
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

// Integration must preserve file contents and keep new empty commits.
func TestQuickContentMergeKeepsLocalWork(t *testing.T) {
	for _, tc := range []struct {
		name       string
		local      string
		integrated string
		emptyTail  bool
		merged     lopper.MergeKind
	}{
		{"different whitespace", "value = 1\n", "value=1\n", false, lopper.NotMerged},
		{"different binary content", "\x00local\n", "\x00other\n", false, lopper.NotMerged},
		{"binary squash", "\x00local\n", "\x00local\n", false, lopper.MergedSquash},
		{"empty file", "", "", false, lopper.MergedSquash},
		{"empty commit after squash", "change\n", "change\n", true, lopper.NotMerged},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := setup(t)
			wt := addWorktree(t, repo, "-b", "feature")
			writeFile(t, filepath.Join(wt.Path, "change.txt"), tc.local)
			git(t, wt.Path, "add", "change.txt")
			git(t, wt.Path, "commit", "-q", "-m", "local")
			writeFile(t, filepath.Join(repo, "change.txt"), tc.integrated)
			git(t, repo, "add", "change.txt")
			git(t, repo, "commit", "-q", "-m", "integrated")
			unpushed := 1
			if tc.emptyTail {
				git(t, wt.Path, "commit", "-q", "--allow-empty", "-m", "local empty")
				unpushed = 2
			}
			wantFacts(t, quick(t, wt), 0, unpushed, tc.merged)
		})
	}
}

// Matching a merge's parent commits does not cover changes made in the merge.
// A squash of the full branch does cover them.
func TestQuickSquashCoversMergeResolution(t *testing.T) {
	repo := setup(t)
	initial := git(t, repo, "rev-parse", "HEAD")
	wt := addWorktree(t, repo, "-b", "feature")
	writeFile(t, filepath.Join(wt.Path, "feature.txt"), "feature\n")
	git(t, wt.Path, "add", "feature.txt")
	git(t, wt.Path, "commit", "-q", "-m", "feature")
	git(t, repo, "checkout", "-q", "-b", "side")
	writeFile(t, filepath.Join(repo, "side.txt"), "side\n")
	git(t, repo, "add", "side.txt")
	git(t, repo, "commit", "-q", "-m", "side")
	git(t, wt.Path, "merge", "-q", "--no-ff", "side", "-m", "merge")
	writeFile(t, filepath.Join(wt.Path, "resolution.txt"), "only in merge\n")
	git(t, wt.Path, "add", "resolution.txt")
	git(t, wt.Path, "commit", "-q", "--amend", "--no-edit")

	git(t, repo, "checkout", "-q", "main")
	git(t, repo, "cherry-pick", "feature~1", "side")
	f := quick(t, wt)
	if len(f.Errors) != 0 || f.Merged == nil || *f.Merged != lopper.NotMerged {
		t.Fatalf("Merged = %v, Errors = %q, want none without errors", ptr(f.Merged), f.Errors)
	}

	git(t, repo, "reset", "-q", "--hard", initial)
	git(t, repo, "merge", "-q", "--squash", "feature")
	git(t, repo, "commit", "-q", "-m", "squash including resolution")
	wantFacts(t, quick(t, wt), 0, 3, lopper.MergedSquash)
}

func TestQuickContentMergeKeepsTrailingNewlineChange(t *testing.T) {
	repo := setup(t)
	writeFile(t, filepath.Join(repo, "content.txt"), "one\ntwo\nthree\nfour\nfive\nsix\n")
	git(t, repo, "add", "content.txt")
	git(t, repo, "commit", "-q", "-m", "initial content")
	wt := addWorktree(t, repo, "-b", "feature")
	writeFile(t, filepath.Join(wt.Path, "content.txt"), "one\nfeature\nthree\nfour\nfive\nsix\n\n")
	git(t, wt.Path, "add", "content.txt")
	git(t, wt.Path, "commit", "-q", "-m", "feature and blank line")
	writeFile(t, filepath.Join(repo, "content.txt"), "one\nfeature\nthree\nfour\nbase\nsix\n")
	git(t, repo, "add", "content.txt")
	git(t, repo, "commit", "-q", "-m", "feature without blank line")
	wantFacts(t, quick(t, wt), 0, 1, lopper.NotMerged)
}

// countingGit counts the git processes an inspection starts.
type countingGit struct {
	gitx.Runner
	calls *int
}

func (c countingGit) Run(ctx context.Context, dir string, args ...string) (string, error) {
	*c.calls++
	return c.Runner.Run(ctx, dir, args...)
}

func (c countingGit) RunRaw(ctx context.Context, dir, stdin string, args ...string) (string, error) {
	*c.calls++
	return c.Runner.RunRaw(ctx, dir, stdin, args...)
}

// A squash of a branch that touched many files, each changed again on main
// since, takes one text merge per file but not a lookup and reads per file.
func TestQuickSquashOfManyFilesStartsFewProcesses(t *testing.T) {
	const files = 100
	repo := setup(t)
	for i := range files {
		writeFile(t, filepath.Join(repo, fmt.Sprintf("f%03d.txt", i)), "one\ntwo\nthree\nfour\nfive\nsix\n")
	}
	git(t, repo, "add", ".")
	git(t, repo, "commit", "-q", "-m", "files")
	wt := addWorktree(t, repo, "-b", "feature")
	for i := range files {
		writeFile(t, filepath.Join(wt.Path, fmt.Sprintf("f%03d.txt", i)), "one\nfeature\nthree\nfour\nfive\nsix\n")
	}
	git(t, wt.Path, "commit", "-q", "-am", "feature")
	git(t, repo, "merge", "-q", "--squash", "feature")
	git(t, repo, "commit", "-q", "-m", "squash")
	for i := range files {
		writeFile(t, filepath.Join(repo, fmt.Sprintf("f%03d.txt", i)), "one\nfeature\nthree\nfour\nbase\nsix\n")
	}
	git(t, repo, "commit", "-q", "-am", "later")

	var calls int
	f := (Inspector{Git: countingGit{gitx.Exec{}, &calls}}).Quick(t.Context(), wt)
	wantFacts(t, f, 0, 1, lopper.MergedSquash)
	if calls > files+20 {
		t.Errorf("inspection started %d git processes for %d files, want at most %d", calls, files, files+20)
	}
}

// A file too large to merge in memory keeps the worktree, even when base
// has its changes.
func TestQuickSquashOfLargeFileIsNotMerged(t *testing.T) {
	var b strings.Builder
	for i := 0; b.Len() <= maxMergeSize; i++ {
		fmt.Fprintf(&b, "line %d\n", i)
	}
	large := b.String()
	repo := setup(t)
	writeFile(t, filepath.Join(repo, "large.txt"), "one\ntwo\nthree\nfour\nfive\n"+large)
	git(t, repo, "add", "large.txt")
	git(t, repo, "commit", "-q", "-m", "large")
	wt := addWorktree(t, repo, "-b", "feature")
	writeFile(t, filepath.Join(wt.Path, "large.txt"), "feature\ntwo\nthree\nfour\nfive\n"+large)
	git(t, wt.Path, "commit", "-q", "-am", "feature")
	git(t, repo, "merge", "-q", "--squash", "feature")
	git(t, repo, "commit", "-q", "-m", "squash")
	writeFile(t, filepath.Join(repo, "large.txt"), "feature\ntwo\nthree\nfour\nfive\n"+large+"base\n")
	git(t, repo, "commit", "-q", "-am", "later")

	wantFacts(t, quick(t, wt), 0, 1, lopper.NotMerged)
}

type failingHistoryGit struct{ gitx.Runner }

func (f failingHistoryGit) Run(ctx context.Context, dir string, args ...string) (string, error) {
	if len(args) > 0 && args[0] == "log" {
		return "", errors.New("commit history unavailable")
	}
	return f.Runner.Run(ctx, dir, args...)
}

// The history of local commits matters only once base has their changes.
func TestQuickHistoryFailureLeavesMergeUnknown(t *testing.T) {
	repo := setup(t)
	wt := addWorktree(t, repo, "-b", "feature")
	writeFile(t, filepath.Join(wt.Path, "change.txt"), "change\n")
	git(t, wt.Path, "add", "change.txt")
	git(t, wt.Path, "commit", "-q", "-m", "local")

	f := (Inspector{Git: failingHistoryGit{gitx.Exec{}}}).Quick(t.Context(), wt)
	if len(f.Errors) != 0 || f.Merged == nil || *f.Merged != lopper.NotMerged {
		t.Fatalf("Merged = %v, Errors = %q, want none without errors", ptr(f.Merged), f.Errors)
	}

	git(t, repo, "merge", "-q", "--squash", "feature")
	git(t, repo, "commit", "-q", "-m", "squash")
	f = (Inspector{Git: failingHistoryGit{gitx.Exec{}}}).Quick(t.Context(), wt)
	if f.Merged != nil || len(f.Errors) != 1 || !strings.Contains(f.Errors[0], "commit history unavailable") {
		t.Fatalf("Merged = %v, Errors = %q, want unknown with history error", ptr(f.Merged), f.Errors)
	}
	if f.Dirty == nil || *f.Dirty != 0 || f.Unpushed == nil || *f.Unpushed != 1 {
		t.Fatalf("Dirty = %v, Unpushed = %v, want 0 and 1", ptr(f.Dirty), ptr(f.Unpushed))
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
