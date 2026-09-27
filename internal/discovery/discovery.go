// Package discovery finds git repositories on disk and lists their
// linked worktrees. It decides *where* worktrees are, never whether
// they are safe to delete.
package discovery

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
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
// including orphaned ones that no repository tracks anymore, and moved
// ones at the path they were moved to.
// emit may be called concurrently. A missing or unreadable root is an
// error; unreadable directories below a root are skipped.
func Scan(ctx context.Context, git gitx.Runner, opts Options, emit func(lopper.Worktree)) error {
	gitDirs := make(chan string, 64)
	var listed sync.Map // real paths of worktrees reported by their repository

	var (
		mu      sync.Mutex
		missing = map[string]lopper.Worktree{} // directory gone, by recordKey
		orphans = map[string]orphan{}          // by real path
	)

	var wg sync.WaitGroup
	for range max(opts.Listers, 1) {
		wg.Go(func() {
			for gitDir := range gitDirs {
				listRepo(ctx, git, gitDir, func(wt lopper.Worktree) {
					// Unless it was moved, see below. git does not call a
					// locked worktree prunable, so the directory is checked.
					if wt.Prunable || isGone(wt.Path) {
						mu.Lock()
						defer mu.Unlock()
						missing[recordKey(gitDir, wt.Path)] = wt
						return
					}
					listed.Store(realPath(wt.Path), struct{}{})
					emit(wt)
				})
			}
		})
	}

	err := findRepos(ctx, opts,
		func(gitDir string) { gitDirs <- gitDir },
		func(dir string, gf gitFile) {
			wt := orphanWorktree(dir, gf.commonDir)
			mu.Lock()
			defer mu.Unlock()
			orphans[realPath(wt.Path)] = orphan{wt, gf}
		})
	close(gitDirs)
	wg.Wait()

	// A worktree whose .git file points to a moved repository still shows up
	// in that repository's list when the walk reached it; only the rest are
	// orphans. A worktree moved to another directory shows up in the list as
	// a missing one, and is reported once, where it is now. Hence both wait
	// for every repository to be listed.
	if ctx.Err() == nil {
		// Sorted, so that when copies of one worktree claim the same
		// missing entry, the same one wins every time.
		for _, key := range slices.Sorted(maps.Keys(orphans)) {
			o := orphans[key]
			if _, ok := listed.Load(key); ok {
				continue
			}
			if o.gf.movedFrom != "" {
				// Another repository may have used and left the same path.
				from := recordKey(o.gf.commonDir, o.gf.movedFrom)
				if stale, ok := missing[from]; ok {
					delete(missing, from)
					emit(movedWorktree(stale, o.wt.Path))
					continue
				}
			}
			emit(o.wt)
		}
		for _, key := range slices.Sorted(maps.Keys(missing)) {
			emit(missing[key])
		}
	}
	return err
}

// orphan is a linked worktree that its repository does not track at its
// path, and what its .git file tells about that repository.
type orphan struct {
	wt lopper.Worktree
	gf gitFile
}

// recordKey identifies a repository's record of a worktree at path.
func recordKey(gitDir, path string) string {
	return realPath(gitDir) + "\x00" + realPath(path)
}

// isGone reports whether path definitely does not exist.
func isGone(path string) bool {
	_, err := os.Lstat(path)
	return errors.Is(err, fs.ErrNotExist)
}

// findRepos reports the common git directory of every repository met
// during the walk, once. A .git directory belongs to a main worktree; a
// .git file leads to a git directory elsewhere, which may live outside
// the roots: that of a linked worktree's main repository, a submodule,
// or a --separate-git-dir or ".bare" layout. Bare repositories have no
// .git at all and are recognized by their worktrees directory. A linked
// worktree whose repository no longer tracks it at its path also goes to
// orphan, with what its .git file tells about the repository.
func findRepos(ctx context.Context, opts Options, found func(gitDir string), orphan func(dir string, gf gitFile)) error {
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
						orphan(filepath.Dir(path), gf)
					}
					if ok && !gf.repoGone {
						// Even without this worktree, the repository may have others.
						report(gf.commonDir)
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

// realPath resolves symlinks so that one directory reached by two paths
// is recognised. Of a path that is gone, what is left is resolved: git
// may record a missing worktree through a symlink the walk does not use.
func realPath(path string) string {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return resolved
	}
	parent := filepath.Dir(path)
	if parent == path {
		return path
	}
	return filepath.Join(realPath(parent), filepath.Base(path))
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

// gitFile is what a .git file tells about its repository.
type gitFile struct {
	commonDir string // the repository's common git directory
	orphaned  bool   // commonDir no longer tracks this worktree here
	movedFrom string // where commonDir expects this worktree instead
	repoGone  bool   // commonDir does not exist at all
}

// readGitFile resolves a .git file ("gitdir: <dir>") to the common git
// directory of the repository it belongs to. For a linked worktree <dir>
// is its admin directory inside the main repository; for a submodule or
// a --separate-git-dir checkout it is the whole git directory.
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
	// repository, even at worktrees/<name>: a submodule's, or one moved
	// away with --separate-git-dir.
	c, err := os.ReadFile(filepath.Join(admin, "commondir")) //nolint:gosec // G703: see above
	if err != nil {
		if isGitDir(admin) {
			return gitFile{commonDir: admin}, true
		}
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
	intact, movedFrom := backLink(admin, dotGit)
	return gitFile{commonDir: common, orphaned: !intact, movedFrom: movedFrom}, true
}

// backLink checks the admin directory's gitdir file, the link `git
// worktree repair` fixes: whether it names dotGit and, when it names a
// .git file that is gone, the directory git still expects the worktree
// in. When the link cannot be read for any reason other than being gone,
// it is assumed intact.
func backLink(admin, dotGit string) (intact bool, movedFrom string) {
	content, err := os.ReadFile(filepath.Join(admin, "gitdir")) //nolint:gosec // G703: paths git wrote, see readGitFile
	if err != nil {
		return !errors.Is(err, fs.ErrNotExist), ""
	}
	target := strings.TrimSpace(string(content))
	if !filepath.IsAbs(target) {
		target = filepath.Join(admin, target) // worktree.useRelativePaths
	}
	target = filepath.Clean(target)
	want, err := os.Stat(target) //nolint:gosec // G703: paths git wrote, see readGitFile
	if errors.Is(err, fs.ErrNotExist) {
		return false, filepath.Dir(target)
	}
	if err != nil {
		return true, ""
	}
	got, err := os.Stat(dotGit)
	// SameFile, not path equality: symlinks, and case on Windows and macOS.
	return err != nil || os.SameFile(want, got), ""
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

// movedWorktree is a worktree that git lists as missing because it now
// lives in dir: what git knows about it, at the path it has now.
func movedWorktree(stale lopper.Worktree, dir string) lopper.Worktree {
	wt := stale
	wt.ID, wt.Path = lopper.ID(dir), dir
	wt.Prunable = false
	wt.MovedFrom = stale.Path
	wt.Origin = classifyOrigin(dir)
	return wt
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
