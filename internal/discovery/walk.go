package discovery

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"

	"github.com/charlievieth/fastwalk"
)

// findRepos reports the common git directory of every repository met
// during the walk, once. A .git directory belongs to a main worktree; a
// .git file leads to a git directory elsewhere, which may live outside
// the roots: that of a linked worktree's main repository, a submodule,
// or a --separate-git-dir or ".bare" layout. Bare repositories have no
// .git at all and are recognized by their worktrees directory. The
// repositories of submodules live inside another git directory, where the
// walk does not go, and are looked up there: their checkout may be gone
// while their worktrees are not. Every linked worktree met also goes to
// linked, with what its .git file tells about the repository.
//
// Before the walk, the places of opts.Known below the roots are looked at
// as the walk would look at them, and what they lead to is reported as
// known; revisited is called after them. The walk then finds the same
// repositories, so they change when a repository is reported, not which.
// Once the walk is over, findRepos returns where it met each repository,
// for a later walk to take as known: its own directory, rather than the
// .git file of a worktree that may be removed, when it met both.
//
// Symbolic links to directories are followed. Every physical directory
// is walked once, whichever path reaches it first, so neither a link
// back to an ancestor nor overlapping roots make the walk repeat itself.
// On Windows a directory that a link and the walk both reach, or a root
// inside another, may be walked twice, but never in a loop.
func findRepos(ctx context.Context, opts Options, found func(gitDir string, known bool), linked func(dir string, gf gitFile), revisited func()) (met []string, err error) {
	var (
		seen   sync.Map
		mu     sync.Mutex
		places = map[string]place{} // where each repository was met, by real git directory
	)
	var report func(gitDir string, at place, known bool)
	report = func(gitDir string, at place, known bool) {
		// The same repo may be reached via a symlinked path.
		resolved := realPath(gitDir)
		if at.path != "" {
			mu.Lock()
			if old, ok := places[resolved]; !ok || at.own && !old.own {
				places[resolved] = at
			}
			mu.Unlock()
		}
		if _, dup := seen.LoadOrStore(resolved, struct{}{}); !dup {
			found(gitDir, known)
			submodules(gitDir, func(sub string) { report(sub, place{}, known) })
		}
	}
	// dotGit looks at a .git entry of type typ at path.
	dotGit := func(path string, typ fs.FileMode, known bool) {
		if typ.IsDir() {
			report(path, place{path, true}, known)
			return
		}
		if !typ.IsRegular() {
			return
		}
		gf, ok := readGitFile(path)
		if ok && gf.admin != gf.commonDir {
			linked(physical(opts.Roots, filepath.Dir(path)), gf)
		}
		if ok && !gf.repoGone {
			// Even without this worktree, the repository may have others.
			report(gf.commonDir, place{path, false}, known)
		}
	}

	for _, path := range opts.Known {
		if ctx.Err() != nil {
			break
		}
		if !below(opts.Roots, path) {
			continue // the walk would not meet it
		}
		info, err := os.Stat(path)
		switch {
		case err != nil:
		case filepath.Base(path) == ".git":
			dotGit(path, info.Mode().Type(), true)
		case info.IsDir() && isBare(path):
			report(path, place{path, true}, true)
		}
	}
	revisited()

	visited := fastwalk.NewEntryFilter() // by device and inode, across roots
	// Only a link or a root leads to a directory twice: below them the walk
	// follows a tree. Telling a directory apart is a stat on Unix, so every
	// one is checked, sparing a second walk of a linked directory. Windows
	// opens each one for it, so there only links and roots are checked.
	everyDir := runtime.GOOS != "windows"
	conf := fastwalk.DefaultConfig
	conf.ToSlash = false // keep native separators under MSYS/Git Bash: paths are reported as found

	for _, root := range opts.Roots {
		root = filepath.Clean(root)
		if err := checkRoot(root); err != nil {
			return nil, err
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
			typ, link := d.Type(), d.Type()&fs.ModeSymlink != 0
			if link {
				info, err := fastwalk.StatDirEntry(path, d)
				if err != nil {
					return nil //nolint:nilerr // dangling, or a loop of links: nothing to walk
				}
				typ = info.Mode().Type()
			}
			if typ.IsDir() && (everyDir || link || path == root) && visited.Entry(path, d) {
				return fs.SkipDir
			}
			name := d.Name()
			if name == ".git" {
				dotGit(path, typ, false)
				if typ.IsDir() {
					return fs.SkipDir
				}
				return nil
			}
			if name == "worktrees" && typ.IsDir() && isGitDir(filepath.Dir(path)) {
				report(filepath.Dir(path), place{filepath.Dir(path), true}, false) // a bare repository
				return fs.SkipDir
			}
			if link && typ.IsDir() {
				return fastwalk.ErrTraverseLink
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	for _, at := range places {
		met = append(met, at.path)
	}
	slices.Sort(met)
	return met, nil
}

// place is where the walk met a repository: own when it is the
// repository's own directory, its .git directory or a bare repository.
type place struct {
	path string
	own  bool
}

// isBare reports whether dir is a bare repository the walk would meet,
// by its worktrees directory.
func isBare(dir string) bool {
	info, err := os.Stat(filepath.Join(dir, "worktrees"))
	return err == nil && info.IsDir() && isGitDir(dir)
}

// submodules calls found for every submodule repository kept in gitDir:
// in modules/<submodule path>, in modules/ of those for nested ones (via
// found), and in worktrees/<id>/modules for those of linked worktrees.
func submodules(gitDir string, found func(gitDir string)) {
	modules(filepath.Join(gitDir, "modules"), found)
	ids, _ := os.ReadDir(filepath.Join(gitDir, "worktrees"))
	for _, id := range ids {
		if id.IsDir() {
			modules(filepath.Join(gitDir, "worktrees", id.Name(), "modules"), found)
		}
	}
}

// modules finds the repositories below dir. A submodule path has several
// components when the submodule is not at the top of its superproject.
// Symbolic links are not followed: git creates none there.
func modules(dir string, found func(gitDir string)) {
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		path := filepath.Join(dir, e.Name())
		if isGitDir(path) {
			found(path)
		} else {
			modules(path, found)
		}
	}
}

// physical is where dir really is, so that a directory the walk may reach
// by several paths is reported the same way every time: its resolved
// path, but below the first root that contains it, as that root is given.
func physical(roots []string, dir string) string {
	resolved := realPath(dir)
	for _, root := range roots {
		if rel, ok := within(realPath(root), resolved); ok {
			return filepath.Join(filepath.Clean(root), rel)
		}
	}
	return resolved
}

// below reports whether a walk of roots reaches path by its name, as
// the roots are given.
func below(roots []string, path string) bool {
	return slices.ContainsFunc(roots, func(root string) bool {
		_, ok := within(filepath.Clean(root), path)
		return ok
	})
}

// within returns path relative to dir, if it is dir or below it.
func within(dir, path string) (string, bool) {
	rel, err := filepath.Rel(dir, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	return rel, true
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
