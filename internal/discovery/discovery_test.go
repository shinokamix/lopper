package discovery

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/shinokamix/lopper/internal/lopper"
)

// fakeGit answers `git worktree list` from a canned listing per directory
// and records every directory it was asked about.
type fakeGit struct {
	mu    sync.Mutex
	lists map[string][]string // repo dir -> linked worktree paths
	fail  map[string]error    // repo dir -> error of git
	calls []string
}

func (f *fakeGit) RunRaw(ctx context.Context, dir string, args ...string) (string, error) {
	return f.Run(ctx, dir, args...)
}

func (f *fakeGit) Run(_ context.Context, dir string, args ...string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(args) > 0 && args[0] == "--work-tree="+dir {
		args = args[1:] // as git does, for a repository without a checkout
	}
	if len(args) < 2 || args[0] != "worktree" || args[1] != "list" {
		return "", errors.New("unsupported")
	}
	f.calls = append(f.calls, dir)
	if err := f.fail[dir]; err != nil {
		return "", err
	}
	// Records work for both plain and -z porcelain output.
	sep := "\n"
	if slices.Contains(args, "-z") {
		sep = "\x00"
	}
	var b strings.Builder
	for _, path := range append([]string{dir}, f.lists[dir]...) {
		b.WriteString("worktree " + path + sep + "HEAD 1" + sep + "branch refs/heads/" + filepath.Base(path) + sep + sep)
	}
	return b.String(), nil
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func mkdir(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
}

// linkWorktree lays out what `git worktree add` leaves on disk.
func linkWorktree(t *testing.T, main, wt string) {
	t.Helper()
	gitDir(t, filepath.Join(main, ".git"))
	admin := filepath.Join(main, ".git", "worktrees", filepath.Base(wt))
	write(t, filepath.Join(admin, "HEAD"), "ref: refs/heads/"+filepath.Base(wt)+"\n")
	write(t, filepath.Join(admin, "commondir"), "../..\n")
	write(t, filepath.Join(admin, "gitdir"), filepath.Join(wt, ".git")+"\n")
	write(t, filepath.Join(wt, ".git"), "gitdir: "+admin+"\n")
}

// gitDir makes dir a repository as far as git's is_git_directory is concerned.
func gitDir(t *testing.T, dir string) {
	t.Helper()
	write(t, filepath.Join(dir, "HEAD"), "ref: refs/heads/main\n")
	mkdir(t, filepath.Join(dir, "objects"))
	mkdir(t, filepath.Join(dir, "refs"))
}

func scan(t *testing.T, git *fakeGit, opts Options) []lopper.Worktree {
	t.Helper()
	var (
		mu    sync.Mutex
		found []lopper.Worktree
	)
	err := Scan(t.Context(), git, opts, func(wt lopper.Worktree) {
		mu.Lock()
		defer mu.Unlock()
		found = append(found, wt)
	})
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	return found
}

func TestScanSpawnsGitOnlyForReposWithWorktrees(t *testing.T) {
	root := t.TempDir()
	mkdir(t, filepath.Join(root, "plain", ".git"))
	mkdir(t, filepath.Join(root, "emptied", ".git", "worktrees"))
	multi := filepath.Join(root, "multi")
	linkWorktree(t, multi, filepath.Join(root, "multi-wt"))

	git := &fakeGit{lists: map[string][]string{multi: {filepath.Join(root, "multi-wt")}}}
	found := scan(t, git, Options{Roots: []string{root}})

	if !slices.Equal(git.calls, []string{multi}) {
		t.Errorf("git worktree list ran in %v, want only %s", git.calls, multi)
	}
	if len(found) != 1 || found[0].Path != filepath.Join(root, "multi-wt") || found[0].Repo.Path != multi {
		t.Errorf("found %+v, want multi-wt of %s", found, multi)
	}
}

func TestScanFollowsLinkedWorktreeToMainRepo(t *testing.T) {
	dir := t.TempDir()
	main := filepath.Join(dir, "main")
	elsewhere := filepath.Join(dir, "elsewhere")
	wt := filepath.Join(elsewhere, "wt")
	linkWorktree(t, main, wt)
	// A submodule's .git file points at a whole repository, which has no
	// linked worktrees of its own: no git process for it.
	write(t, filepath.Join(main, ".git", "modules", "sub", "HEAD"), "ref: refs/heads/main\n")
	mkdir(t, filepath.Join(main, ".git", "modules", "sub", "objects"))
	mkdir(t, filepath.Join(main, ".git", "modules", "sub", "refs"))
	write(t, filepath.Join(elsewhere, "sub", ".git"), "gitdir: ../../main/.git/modules/sub\n")

	for _, roots := range [][]string{{elsewhere}, {dir}, {elsewhere, main}} {
		git := &fakeGit{lists: map[string][]string{main: {wt}}}
		found := scan(t, git, Options{Roots: roots})
		if !slices.Equal(git.calls, []string{main}) {
			t.Errorf("roots %v: git worktree list ran in %v, want once in %s", roots, git.calls, main)
		}
		if len(found) != 1 || found[0].Path != wt {
			t.Errorf("roots %v: found %+v, want %s", roots, found, wt)
		}
	}
}

func TestScanReportsOrphanedWorktrees(t *testing.T) {
	dir := t.TempDir()
	gone := filepath.Join(dir, "gone")
	orphan := filepath.Join(dir, "agent", "orphan")
	write(t, filepath.Join(orphan, ".git"), "gitdir: "+filepath.Join(gone, ".git", "worktrees", "orphan")+"\n")
	// A stray file named HEAD above the repository does not make a git directory.
	write(t, filepath.Join(dir, "HEAD"), "")
	// A submodule whose repository is gone is not a worktree.
	write(t, filepath.Join(dir, "agent", "sub", ".git"), "gitdir: "+filepath.Join(gone, ".git", "modules", "sub")+"\n")
	// A repository moved after `git worktree add`: the worktree's .git file
	// is stale, but the repository still tracks it.
	moved := filepath.Join(dir, "moved")
	mkdir(t, filepath.Join(moved, ".git", "worktrees", "wt"))
	wt := filepath.Join(dir, "agent", "wt")
	write(t, filepath.Join(wt, ".git"), "gitdir: "+filepath.Join(dir, "old", ".git", "worktrees", "wt")+"\n")

	// Overlapping roots must not report the orphan twice.
	git := &fakeGit{lists: map[string][]string{moved: {wt}}}
	found := scan(t, git, Options{Roots: []string{dir, filepath.Join(dir, "agent")}})

	want := map[string]lopper.Worktree{
		orphan: {ID: lopper.ID(orphan), Path: orphan, Repo: lopper.Repo{Path: gone}, Orphaned: true, Origin: lopper.OriginManual},
		wt:     {ID: lopper.ID(wt), Path: wt, Repo: lopper.Repo{Path: moved}, Branch: "wt", Head: "1", Origin: lopper.OriginManual},
	}
	if len(found) != len(want) {
		t.Fatalf("found %+v, want %d worktrees", found, len(want))
	}
	for _, got := range found {
		if got != want[got.Path] {
			t.Errorf("found %+v, want %+v", got, want[got.Path])
		}
	}
}

// Caches and dependency trees are walked too: agents and tools create
// worktrees there, and a worktree nobody sees is never cleaned up.
func TestScanWalksEveryDirectory(t *testing.T) {
	root := t.TempDir()
	lib := filepath.Join(root, "Library", "Caches", "repo")
	dep := filepath.Join(root, "code", "node_modules", "dep")
	linkWorktree(t, lib, lib+"-wt")
	linkWorktree(t, dep, dep+"-wt")

	git := &fakeGit{}
	scan(t, git, Options{Roots: []string{root}})
	if got, want := slices.Sorted(slices.Values(git.calls)), []string{lib, dep}; !slices.Equal(got, want) {
		t.Errorf("git worktree list ran in %v, want %v", got, want)
	}
}

// A worktree found through its .git file stays in the results when its
// repository cannot be listed, marked as unconfirmed and with the branch
// its admin directory names.
func TestScanKeepsWorktreesGitCannotList(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	wt := filepath.Join(root, "wt")
	linkWorktree(t, repo, wt)
	write(t, filepath.Join(repo, ".git", "worktrees", "wt", "HEAD"), "ref: refs/heads/feature\n")

	git := &fakeGit{fail: map[string]error{repo: errors.New("git worktree list: exit status 128: fatal: bad config line 1\nhint: more")}}
	found := scan(t, git, Options{Roots: []string{root}})

	want := lopper.Worktree{
		ID: lopper.ID(wt), Path: wt, Repo: lopper.Repo{Path: repo}, Branch: "feature",
		Unconfirmed: "could not list its worktrees: git worktree list: exit status 128: fatal: bad config line 1",
		Origin:      lopper.OriginManual,
	}
	if len(found) != 1 || found[0] != want {
		t.Errorf("found %+v, want %+v", found, want)
	}
}

// The repository of a nested submodule lives in the modules directory of
// its parent submodule's repository. Its worktrees are found even when no
// checkout of either submodule is left.
func TestScanFindsNestedSubmoduleRepos(t *testing.T) {
	root := t.TempDir()
	superGit := filepath.Join(root, "super", ".git")
	gitDir(t, superGit)
	outer := filepath.Join(superGit, "modules", "vendor", "outer")
	gitDir(t, outer)
	inner := filepath.Join(outer, "modules", "inner")
	gitDir(t, inner)
	wt := filepath.Join(t.TempDir(), "wt")
	mkdir(t, filepath.Join(inner, "worktrees", "wt"))

	git := &fakeGit{lists: map[string][]string{inner: {wt}}}
	found := scan(t, git, Options{Roots: []string{filepath.Join(root, "super")}})
	if len(found) != 1 || found[0].Path != wt || found[0].Repo.Path != inner {
		t.Errorf("found %+v, want %s of %s", found, wt, inner)
	}
}

func symlink(t *testing.T, target, link string) {
	t.Helper()
	mkdir(t, filepath.Dir(link))
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err) // a privilege on Windows
	}
}

func TestScanFollowsSymlinkedDirectories(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	repo := filepath.Join(outside, "repo")
	linkWorktree(t, repo, filepath.Join(outside, "repo-wt"))
	symlink(t, outside, filepath.Join(root, "code"))
	// A worktree whose repository is gone, reached by two paths. The walk
	// meets the link first, yet reports where the worktree really is.
	target := filepath.Join(root, "deep", "er", "real")
	orphan := filepath.Join(target, "orphan")
	write(t, filepath.Join(orphan, ".git"), "gitdir: "+filepath.Join(outside, "gone", ".git", "worktrees", "orphan")+"\n")
	symlink(t, target, filepath.Join(root, "alias"))
	// Two links back to an ancestor: without loop detection, the number
	// of paths doubles with every level.
	symlink(t, root, filepath.Join(root, "deep", "up"))
	symlink(t, root, filepath.Join(target, "up"))

	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	// Reached through the link, the repository lists its worktree.
	git := &fakeGit{lists: map[string][]string{
		filepath.Join(root, "code", "repo"): {filepath.Join(outside, "repo-wt")},
	}}
	var found []lopper.Worktree
	var mu sync.Mutex
	err := Scan(ctx, git, Options{Roots: []string{root}}, func(wt lopper.Worktree) {
		mu.Lock()
		defer mu.Unlock()
		found = append(found, wt)
	})
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(git.calls) != 1 || realPath(git.calls[0]) != realPath(repo) {
		t.Errorf("git worktree list ran in %v, want once in %s", git.calls, repo)
	}
	var orphans []string
	for _, wt := range found {
		if wt.Orphaned {
			orphans = append(orphans, wt.Path)
		}
	}
	if len(found) != 2 || !slices.Equal(orphans, []string{orphan}) {
		t.Errorf("found %+v, want repo-wt and orphan %s", found, orphan)
	}
}

func TestScanRootErrors(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "file")
	write(t, file, "")

	for root, want := range map[string]string{
		filepath.Join(dir, "missing"): "no such directory",
		file:                          "not a directory",
	} {
		err := Scan(t.Context(), &fakeGit{}, Options{Roots: []string{root}}, func(lopper.Worktree) {})
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("Scan(%s) = %v, want error containing %q", root, err, want)
		}
	}
}
