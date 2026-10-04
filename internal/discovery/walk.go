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

// findRepos reports each repository met during the walk once, by its
// common git directory, and passes every linked worktree it meets to
// linked. A .git directory is a main worktree. A .git file leads to a git
// directory that may lie outside the roots: a linked worktree's
// repository, a submodule, or a --separate-git-dir or ".bare" layout. A
// bare repository has no .git and is found by its worktrees directory.
// Submodule repositories live inside a git directory, which the walk
// skips. findRepos looks them up there, because their checkout may be gone
// while their worktrees are not.
//
// findRepos checks the places in opts.Known below the roots first, so it
// reports their repositories early. The walk finds the same set either way.
// The returned places prefer a repository's own directory over a
// worktree's .git file, which may be removed.
//
// Symlinks to directories are followed. Each physical directory is walked
// once, so links to an ancestor and overlapping roots cause no repeats. On
// Windows a directory may be walked twice, but never in a loop.
func findRepos(ctx context.Context, opts Options, found func(gitDir string), linked func(dir string, gf gitFile)) (met []string, err error) {
	var (
		seen   sync.Map
		mu     sync.Mutex
		places = map[string]place{} // where each repository was met, by real git directory
	)
	var report func(gitDir string, at place)
	report = func(gitDir string, at place) {
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
			found(gitDir)
			submodules(gitDir, func(sub string) { report(sub, place{}) })
		}
	}
	dotGit := func(path string, typ fs.FileMode) {
		if typ.IsDir() {
			report(path, place{path, true})
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
			report(gf.commonDir, place{path, false})
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
			dotGit(path, info.Mode().Type())
		case info.IsDir() && isBare(path):
			report(path, place{path, true})
		}
	}

	visited := fastwalk.NewEntryFilter() // by device and inode, across roots
	// Only links and roots lead to a directory twice. On Unix the check is a
	// stat, so every directory gets one and a linked directory the walk
	// reached first is not walked again. On Windows it opens the directory,
	// so only links and roots are checked.
	everyDir := runtime.GOOS != "windows"
	conf := fastwalk.DefaultConfig
	conf.ToSlash = false // report paths as found, even under MSYS or Git Bash

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
				return nil // skip unreadable entries below a root
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
				dotGit(path, typ)
				if typ.IsDir() {
					return fs.SkipDir
				}
				return nil
			}
			if name == "worktrees" && typ.IsDir() && isGitDir(filepath.Dir(path)) {
				report(filepath.Dir(path), place{filepath.Dir(path), true}) // a bare repository
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

// place is where the walk met a repository. own marks the repository's own
// directory, which is its .git directory or a bare repository.
type place struct {
	path string
	own  bool
}

// isBare reports whether dir is a bare repository that the walk would find
// by its worktrees directory.
func isBare(dir string) bool {
	info, err := os.Stat(filepath.Join(dir, "worktrees"))
	return err == nil && info.IsDir() && isGitDir(dir)
}

// submodules calls found for every submodule repository in gitDir, under
// modules/<submodule path> and, for linked worktrees, under
// worktrees/<id>/modules. Nested submodules live in modules/ of their
// parent's repository, which found reaches in turn.
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
// components when the submodule sits in a subdirectory of its superproject.
// modules does not follow symlinks, since git creates none there.
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

// physical names dir the same way whichever path the walk took. It resolves
// dir, but under the first root that contains it, spelled as that root is
// given.
func physical(roots []string, dir string) string {
	resolved := realPath(dir)
	for _, root := range roots {
		if rel, ok := within(realPath(root), resolved); ok {
			return filepath.Join(filepath.Clean(root), rel)
		}
	}
	return resolved
}

// below reports whether a walk of roots reaches path by name, without
// resolving symlinks.
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

// realPath resolves symlinks, so two paths to one directory compare equal.
// For a missing path it resolves the part that exists, because git may
// record a missing worktree through a symlink the walk does not use.
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
