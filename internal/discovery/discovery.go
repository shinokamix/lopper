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

// Scan walks opts.Roots and calls emit for every linked worktree found,
// including orphaned ones that no repository tracks anymore.
// emit may be called concurrently. A missing or unreadable root is an
// error; unreadable directories below a root are skipped.
func Scan(ctx context.Context, git gitx.Runner, opts Options, emit func(lopper.Worktree)) error {
	gitDirs := make(chan string, 64)
	var listed sync.Map // real paths of worktrees reported by their repository

	var wg sync.WaitGroup
	for range max(opts.Listers, 1) {
		wg.Go(func() {
			for gitDir := range gitDirs {
				listRepo(ctx, git, gitDir, func(wt lopper.Worktree) {
					listed.Store(realPath(wt.Path), struct{}{})
					emit(wt)
				})
			}
		})
	}

	var (
		mu      sync.Mutex
		orphans = map[string]lopper.Worktree{} // by real path
	)
	err := findRepos(ctx, opts,
		func(gitDir string) { gitDirs <- gitDir },
		func(wt lopper.Worktree) {
			mu.Lock()
			defer mu.Unlock()
			orphans[realPath(wt.Path)] = wt
		})
	close(gitDirs)
	wg.Wait()

	// A worktree whose .git file points to a moved repository still shows up
	// in that repository's list when the walk reached it; only the rest are
	// orphans. Hence they wait for every repository to be listed.
	if ctx.Err() == nil {
		for key, wt := range orphans {
			if _, ok := listed.Load(key); !ok {
				emit(wt)
			}
		}
	}
	return err
}

// findRepos reports the common git directory of every repository met
// during the walk, once. A .git directory belongs to a main worktree; a
// .git file belongs to a linked worktree (or a submodule) and leads to
// its main repository, which may live outside the roots. A linked
// worktree whose repository no longer tracks it also goes to orphan.
func findRepos(ctx context.Context, opts Options, found func(gitDir string), orphan func(lopper.Worktree)) error {
	var seen sync.Map
	report := func(gitDir string) {
		// The same repo may be reached via a symlinked path.
		if _, dup := seen.LoadOrStore(realPath(gitDir), struct{}{}); !dup {
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
					gf, ok := readGitFile(path)
					if ok && gf.orphaned {
						orphan(orphanWorktree(filepath.Dir(path), gf.commonDir))
					}
					if ok && !gf.repoGone {
						// Even without this worktree, the repository may have others.
						report(gf.commonDir)
					}
				}
				return nil
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

// realPath resolves symlinks so that one directory reached by two paths
// is recognised; a path that cannot be resolved is used as is.
func realPath(path string) string {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return resolved
	}
	return path
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

// gitFile is what a linked worktree's .git file tells about its repository.
type gitFile struct {
	commonDir string // the repository's common git directory
	orphaned  bool   // commonDir no longer tracks this worktree
	repoGone  bool   // commonDir does not exist at all
}

// readGitFile resolves a linked worktree's .git file ("gitdir: <admin>")
// to the common git directory of its main repository. Submodules also
// use a .git file, but their admin directory is a whole repository
// rather than a linked worktree; they are ignored, as is anything else
// that is not a linked worktree.
func readGitFile(dotGit string) (gitFile, bool) {
	content, err := os.ReadFile(dotGit)
	if err != nil {
		return gitFile{}, false
	}
	admin, ok := strings.CutPrefix(strings.TrimSpace(string(content)), "gitdir:")
	if !ok {
		return gitFile{}, false
	}
	admin = strings.TrimSpace(admin)
	if !filepath.IsAbs(admin) {
		admin = filepath.Join(filepath.Dir(dotGit), admin)
	}
	admin = filepath.Clean(admin)

	// Following the paths git wrote is the point: they may lead anywhere.
	// Only a definite "does not exist" makes an orphan: a permission error
	// says nothing about the repository.
	if _, err := os.Stat(admin); errors.Is(err, fs.ErrNotExist) { //nolint:gosec // G703: see above
		return orphanGitFile(admin)
	}

	// `git worktree add` always writes commondir, and git finds the common
	// directory through it. An admin directory without one is a whole
	// repository, a submodule's, even at worktrees/<name>.
	c, err := os.ReadFile(filepath.Join(admin, "commondir")) //nolint:gosec // G703: see above
	if err != nil {
		return gitFile{}, false
	}
	common := strings.TrimSpace(string(c))
	if !filepath.IsAbs(common) {
		common = filepath.Join(admin, common)
	}
	common = filepath.Clean(common)

	if info, err := os.Stat(common); err != nil || !info.IsDir() { //nolint:gosec // G703: see above
		return gitFile{}, false
	}
	// The admin directory may now belong to another worktree (git reuses a
	// name freed by a prune), or this one was moved away from where git
	// expects it. Either way the repository no longer tracks this directory.
	return gitFile{commonDir: common, orphaned: !pointsBack(admin, dotGit)}, true
}

// pointsBack reports whether the admin directory's gitdir file names
// dotGit, the link `git worktree repair` checks. When that cannot be read
// for any reason other than being gone, the link is assumed intact.
func pointsBack(admin, dotGit string) bool {
	content, err := os.ReadFile(filepath.Join(admin, "gitdir")) //nolint:gosec // G703: paths git wrote, see readGitFile
	if err != nil {
		return !errors.Is(err, fs.ErrNotExist)
	}
	target := strings.TrimSpace(string(content))
	if !filepath.IsAbs(target) {
		target = filepath.Join(admin, target) // worktree.useRelativePaths
	}
	want, err := os.Stat(target) //nolint:gosec // G703: paths git wrote, see readGitFile
	if err != nil {
		return !errors.Is(err, fs.ErrNotExist)
	}
	got, err := os.Stat(dotGit)
	// SameFile, not path equality: symlinks, and case on Windows and macOS.
	return err != nil || os.SameFile(want, got)
}

// orphanGitFile explains a .git file whose admin directory is gone. It
// must not mistake a submodule for a worktree: a submodule checked out at
// worktrees/<name> keeps its git directory at
// <super>/.git/modules/worktrees/<name>, which looks the same.
func orphanGitFile(admin string) (gitFile, bool) {
	common := filepath.Dir(filepath.Dir(admin))
	if filepath.Base(filepath.Dir(admin)) != "worktrees" {
		return gitFile{}, false
	}
	info, err := os.Stat(common) //nolint:gosec // G703: paths git wrote, see readGitFile
	if errors.Is(err, fs.ErrNotExist) {
		// Nothing left to check. A repository inside another git directory
		// is most likely that submodule layout, so it is not trusted.
		if insideGitDir(common) {
			return gitFile{}, false
		}
		return gitFile{commonDir: common, orphaned: true, repoGone: true}, true
	}
	// <super>/.git/modules is not a repository, while a submodule's own
	// repository (<super>/.git/modules/<name>) is and may have worktrees.
	if err != nil || !info.IsDir() || !isGitDir(common) {
		return gitFile{}, false
	}
	return gitFile{commonDir: common, orphaned: true}, true
}

// insideGitDir reports whether path lies below a git directory: one named
// .git, even if it is gone, or any existing one, such as a superproject's
// --separate-git-dir.
func insideGitDir(path string) bool {
	for dir := filepath.Dir(path); ; dir = filepath.Dir(dir) {
		if filepath.Base(dir) == ".git" || isGitDir(dir) {
			return true
		}
		if filepath.Dir(dir) == dir {
			return false
		}
	}
}

// isGitDir mirrors git's own test (is_git_directory in setup.c): a
// repository has HEAD, objects and refs.
func isGitDir(dir string) bool {
	for _, name := range []string{"HEAD", "objects", "refs"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil { //nolint:gosec // G703: paths git wrote, see readGitFile
			return false
		}
	}
	return true
}

// orphanWorktree describes a worktree from its directory alone: with the
// admin directory gone, its branch and HEAD are unknown.
func orphanWorktree(dir, commonDir string) lopper.Worktree {
	path := filepath.Clean(dir)
	return lopper.Worktree{
		ID:       lopper.ID(path),
		Path:     path,
		Repo:     lopper.Repo{Path: repoPath(commonDir)},
		Orphaned: true,
		Origin:   classifyOrigin(path),
	}
}

// repoPath is the main worktree of a repository, or the git directory
// itself for a bare repository.
func repoPath(gitDir string) string {
	if filepath.Base(gitDir) == ".git" {
		return filepath.Dir(gitDir)
	}
	return gitDir
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
	// Run git from the main worktree when there is one.
	dir := repoPath(gitDir)
	entries, err := gitx.ListWorktrees(ctx, git, dir)
	if err != nil || len(entries) < 2 {
		return // not a repo we can read, or no linked worktrees
	}
	repo := lopper.Repo{
		Path:          dir,
		DefaultBranch: gitx.DefaultBranch(ctx, git, dir),
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
