// Package discovery finds git repositories on disk and lists their
// linked worktrees. It decides *where* worktrees are, never whether
// they are safe to delete.
package discovery

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/charlievieth/fastwalk"

	"github.com/shinokamix/lopper/internal/gitx"
	"github.com/shinokamix/lopper/internal/lopper"
)

// Options control a discovery run.
type Options struct {
	Roots     []string
	SkipNames map[string]bool // directory base names never descended into, at any depth
	SkipPaths map[string]bool // absolute, cleaned directory paths never descended into
	Listers   int             // concurrent `git worktree list` processes; at least 1
}

// Scan walks opts.Roots and calls emit for every linked worktree found.
// emit may be called concurrently. A missing or unreadable root is an
// error; unreadable directories below a root are skipped.
func Scan(ctx context.Context, git gitx.Runner, opts Options, emit func(lopper.Worktree)) error {
	gitDirs := make(chan string, 64)

	var wg sync.WaitGroup
	for range max(opts.Listers, 1) {
		wg.Go(func() {
			for gitDir := range gitDirs {
				listRepo(ctx, git, gitDir, emit)
			}
		})
	}

	err := findRepos(ctx, opts, func(gitDir string) { gitDirs <- gitDir })
	close(gitDirs)
	wg.Wait()
	return err
}

// findRepos reports the common git directory of every repository met
// during the walk, once. A .git directory belongs to a main worktree; a
// .git file leads to a git directory elsewhere, which may live outside
// the roots: that of a linked worktree's main repository, a submodule,
// or a --separate-git-dir or ".bare" layout. Bare repositories have no
// .git at all and are recognized by their worktrees directory.
func findRepos(ctx context.Context, opts Options, found func(gitDir string)) error {
	var seen sync.Map
	report := func(gitDir string) {
		key := gitDir
		if resolved, err := filepath.EvalSymlinks(gitDir); err == nil {
			key = resolved // the same repo may be reached via a symlinked path
		}
		if _, dup := seen.LoadOrStore(key, struct{}{}); !dup {
			found(gitDir)
		}
	}
	conf := fastwalk.DefaultConfig
	conf.ToSlash = false // keep native separators under MSYS/Git Bash, or SkipPaths never match

	for _, root := range opts.Roots {
		root = filepath.Clean(root)
		if err := checkRoot(root); err != nil {
			return err
		}
		err := fastwalk.Walk(&conf, root, func(path string, d fs.DirEntry, err error) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			if err != nil {
				if path == root {
					return fmt.Errorf("scan %s: %w", root, err)
				}
				return nil // unreadable entries below a root are skipped, not fatal
			}
			name := d.Name()
			if name == ".git" {
				if d.IsDir() {
					report(path)
					return fs.SkipDir
				}
				if d.Type().IsRegular() {
					if gitDir, ok := commonGitDir(path); ok {
						report(gitDir)
					}
				}
				return nil
			}
			if name == "worktrees" && d.IsDir() && isGitDir(filepath.Dir(path)) {
				report(filepath.Dir(path)) // a bare repository
				return fs.SkipDir
			}
			if d.IsDir() && path != root && (opts.SkipNames[name] || opts.SkipPaths[path]) {
				return fs.SkipDir
			}
			return nil
		})
		if err != nil {
			return err
		}
	}
	return nil
}

func checkRoot(root string) error {
	info, err := os.Stat(root)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return fmt.Errorf("%s: no such directory", root)
	case err != nil:
		return fmt.Errorf("scan %s: %w", root, err)
	case !info.IsDir():
		return fmt.Errorf("%s: not a directory", root)
	}
	return nil
}

// commonGitDir resolves a .git file ("gitdir: <dir>") to the common git
// directory of the repository it belongs to. For a linked worktree <dir>
// is its admin directory inside the main repository; for a submodule or
// a --separate-git-dir checkout it is the whole git directory.
func commonGitDir(dotGit string) (string, bool) {
	content, err := os.ReadFile(dotGit)
	if err != nil {
		return "", false
	}
	admin, ok := strings.CutPrefix(strings.TrimSpace(string(content)), "gitdir:")
	if !ok {
		return "", false
	}
	admin = strings.TrimSpace(admin)
	if !filepath.IsAbs(admin) {
		admin = filepath.Join(filepath.Dir(dotGit), admin)
	}
	admin = filepath.Clean(admin)

	var common string
	// Following the paths git wrote is the point: they may lead anywhere.
	c, err := os.ReadFile(filepath.Join(admin, "commondir")) //nolint:gosec // G703: see above
	switch {
	case err == nil:
		common = strings.TrimSpace(string(c))
		if !filepath.IsAbs(common) {
			common = filepath.Join(admin, common)
		}
		common = filepath.Clean(common)
	case isGitDir(admin):
		common = admin
	case filepath.Base(filepath.Dir(admin)) == "worktrees":
		common = filepath.Dir(filepath.Dir(admin))
	default:
		return "", false
	}

	// TODO: report orphaned worktrees whose main repository is gone.
	if info, err := os.Stat(common); err != nil || !info.IsDir() { //nolint:gosec // G703: see above
		return "", false
	}
	return common, true
}

// isGitDir tells whether dir is a repository's own git directory rather
// than a linked worktree's admin directory, which has no object store.
func isGitDir(dir string) bool {
	// dir may come from a path git wrote, see commonGitDir.
	head, err := os.Stat(filepath.Join(dir, "HEAD")) //nolint:gosec // G703
	if err != nil || !head.Mode().IsRegular() {
		return false
	}
	objects, err := os.Stat(filepath.Join(dir, "objects")) //nolint:gosec // G703
	return err == nil && objects.IsDir()
}

// hasLinkedWorktrees tells, without spawning git, whether the repository
// can have linked worktrees at all. Almost none do, so this saves one
// git process per repository on disk.
func hasLinkedWorktrees(gitDir string) bool {
	f, err := os.Open(filepath.Join(gitDir, "worktrees"))
	if err != nil {
		return false
	}
	defer f.Close()
	names, _ := f.Readdirnames(1)
	return len(names) > 0
}

func listRepo(ctx context.Context, git gitx.Runner, gitDir string, emit func(lopper.Worktree)) {
	if !hasLinkedWorktrees(gitDir) {
		return
	}
	// Run git from the main worktree when there is one; bare repositories
	// have only the git directory itself.
	repoPath := gitDir
	if filepath.Base(gitDir) == ".git" {
		repoPath = filepath.Dir(gitDir)
	}
	entries, err := gitx.ListWorktrees(ctx, git, repoPath)
	if err != nil || len(entries) < 2 {
		return // not a repo we can read, or no linked worktrees
	}
	repo := lopper.Repo{
		Path:          repoPath,
		DefaultBranch: gitx.DefaultBranch(ctx, git, repoPath),
	}
	for _, e := range entries[1:] { // entries[0] is the main worktree
		if e.Bare {
			continue
		}
		path := filepath.Clean(e.Path)
		emit(lopper.Worktree{
			ID:       lopper.ID(path),
			Path:     path,
			Repo:     repo,
			Branch:   e.Branch,
			Head:     e.Head,
			Locked:   e.Locked,
			Prunable: e.Prunable,
			Origin:   classifyOrigin(path),
		})
	}
}
