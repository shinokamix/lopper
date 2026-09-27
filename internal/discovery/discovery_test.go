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

	"github.com/shinokamix/lopper/internal/lopper"
)

// fakeGit answers `git worktree list` from a canned listing per directory
// and records every directory it was asked about.
type fakeGit struct {
	mu    sync.Mutex
	lists map[string][]string // repo dir -> linked worktree paths
	calls []string
}

func (f *fakeGit) Run(_ context.Context, dir string, args ...string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(args) < 2 || args[0] != "worktree" || args[1] != "list" {
		return "", errors.New("unsupported")
	}
	f.calls = append(f.calls, dir)
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
	admin := filepath.Join(main, ".git", "worktrees", filepath.Base(wt))
	write(t, filepath.Join(admin, "commondir"), "../..\n")
	write(t, filepath.Join(wt, ".git"), "gitdir: "+admin+"\n")
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
	// A submodule's .git file points at a whole repository: not a worktree.
	mkdir(t, filepath.Join(main, ".git", "modules", "sub", "worktrees", "x"))
	write(t, filepath.Join(elsewhere, "sub", ".git"), "gitdir: ../../main/.git/modules/sub\n")
	// An orphaned worktree whose main repository is gone.
	write(t, filepath.Join(elsewhere, "orphan", ".git"), "gitdir: "+filepath.Join(dir, "gone", ".git", "worktrees", "orphan")+"\n")

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

func TestScanSkipDirs(t *testing.T) {
	root := t.TempDir()
	repos := map[string]bool{ // repo -> expected to be listed
		filepath.Join(root, "Library", "repo"):                  false, // anchored path
		filepath.Join(root, "code", "Library", "repo"):          true,  // same name elsewhere
		filepath.Join(root, "code", "node_modules", "dep"):      false, // skipped name
		filepath.Join(root, "code", "app", "node_modules", "d"): false,
	}
	git := &fakeGit{}
	var want []string
	for repo, listed := range repos {
		linkWorktree(t, repo, repo+"-wt")
		if listed {
			want = append(want, repo)
		}
	}

	scan(t, git, Options{
		Roots:     []string{root},
		SkipNames: map[string]bool{"node_modules": true},
		SkipPaths: map[string]bool{filepath.Join(root, "Library"): true},
	})
	if !slices.Equal(git.calls, want) {
		t.Errorf("git worktree list ran in %v, want %v", git.calls, want)
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
