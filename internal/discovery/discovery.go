// Package discovery finds git repositories on disk and lists their linked
// worktrees. It never decides whether a worktree is safe to delete.
package discovery

import (
	"context"
	"errors"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/shinokamix/lopper/internal/gitx"
	"github.com/shinokamix/lopper/internal/lopper"
)

// Options control a discovery run.
type Options struct {
	Roots   []string // walked in full, skipping no directory below them
	Listers int      // concurrent `git worktree list` processes; at least 1
	// Known are places, as Met received them, where an earlier scan met
	// repositories. Scan checks those below Roots before the walk, so their
	// repositories are listed first. They change when a scan finds
	// worktrees, never which.
	Known []string
	// Met, if set, receives the places where a scan met repositories, for a
	// later scan's Known. Scan calls it only after a walk that neither failed
	// nor was cancelled.
	Met func(places []string)
}

// Scan walks opts.Roots and calls emit, possibly concurrently, for every
// linked worktree found. That includes orphaned ones no repository tracks,
// moved ones at their new path, and unconfirmed ones whose .git file names a
// repository that did not or could not list them. A missing or unreadable
// root is an error. Scan skips unreadable directories below a root.
func Scan(ctx context.Context, git gitx.Runner, opts Options, emit func(lopper.Worktree)) error {
	gitDirs := make(chan string, 64)
	var listed sync.Map // real paths of worktrees their repository listed

	var (
		mu         sync.Mutex
		missing    = map[string]lopper.Worktree{} // .git file gone, by recordKey
		orphans    = map[string]orphan{}          // by real path
		candidates = map[string]orphan{}          // tracked, says the .git file; by real path
		listErrs   = map[string]string{}          // why `git worktree list` failed, by real git directory
		repos      = map[string]lopper.Repo{}     // repositories git could list, by real git directory
	)

	var wg sync.WaitGroup
	for range max(opts.Listers, 1) {
		wg.Go(func() {
			for gitDir := range gitDirs {
				repo, err := listRepo(ctx, git, gitDir, func(wt lopper.Worktree) {
					if !present(&wt) { // unless it was moved, see below
						mu.Lock()
						defer mu.Unlock()
						missing[recordKey(gitDir, wt.Path)] = wt
						return
					}
					listed.Store(realPath(wt.Path), struct{}{})
					emit(wt)
				})
				mu.Lock()
				if err != nil {
					listErrs[realPath(gitDir)] = firstLine(err.Error())
				} else if repo.Path != "" {
					repos[realPath(gitDir)] = repo
				}
				mu.Unlock()
			}
		})
	}

	met, err := findRepos(ctx, opts,
		func(gitDir string) { gitDirs <- gitDir },
		func(dir string, gf gitFile) {
			mu.Lock()
			defer mu.Unlock()
			if gf.orphaned {
				wt := orphanWorktree(dir, gf.commonDir)
				orphans[realPath(wt.Path)] = orphan{wt, gf}
				return
			}
			candidates[realPath(dir)] = orphan{lopper.Worktree{Path: filepath.Clean(dir)}, gf}
		})
	close(gitDirs)
	wg.Wait()
	if err == nil && ctx.Err() == nil && opts.Met != nil {
		opts.Met(met)
	}

	// Orphans and missing worktrees wait until every repository is listed. A
	// worktree whose .git file points to a moved repository still appears in
	// that repository's list if the walk reached it, so only the rest are
	// orphans. A worktree moved to another directory appears in the list as
	// missing, and Scan reports it once, at its new path.
	if ctx.Err() == nil {
		// Sorted, so the same copy wins every time when copies of one
		// worktree claim the same missing entry.
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
		// Report worktrees whose .git file the repository did not confirm,
		// rather than lose them when git fails.
		for _, key := range slices.Sorted(maps.Keys(candidates)) {
			if _, ok := listed.Load(key); ok {
				continue
			}
			c := candidates[key]
			repo, ok := repos[realPath(c.gf.commonDir)]
			if !ok {
				repo = lopper.Repo{Path: repoPath(c.gf.commonDir)}
			}
			why := "its repository does not list it"
			if err, ok := listErrs[realPath(c.gf.commonDir)]; ok {
				why = "could not list its worktrees: " + err
			}
			if c.gf.damage != "" {
				why = c.gf.damage
			}
			emit(unconfirmedWorktree(c.wt.Path, repo, c.gf.admin, why))
		}
	}
	return err
}

// ErrNotFound is returned by Find and Lookup when path holds no linked
// worktree. A main worktree, a submodule or a plain directory never is one.
var ErrNotFound = errors.New("not a linked worktree")

// Find returns the linked worktree at path as a Scan of opts.Roots reports
// it, whatever its state. The roots must reach path. If its directory is
// gone, only a walk that meets its repository finds it.
func Find(ctx context.Context, git gitx.Runner, opts Options, path string) (lopper.Worktree, error) {
	want := realPath(path)
	var (
		mu    sync.Mutex
		wt    lopper.Worktree
		found bool
	)
	err := Scan(ctx, git, opts, func(w lopper.Worktree) {
		if realPath(w.Path) == want {
			mu.Lock()
			defer mu.Unlock()
			wt, found = w, true
		}
	})
	if err != nil {
		return lopper.Worktree{}, err
	}
	if !found {
		return lopper.Worktree{}, ErrNotFound
	}
	return wt, nil
}

// Lookup returns the linked worktree at path as the repository at repo, a
// lopper.Repo path, lists it now, with the state Scan would give it. Unlike
// Find, it needs no walk, so it can look up a worktree whose directory is
// gone.
func Lookup(ctx context.Context, git gitx.Runner, repo, path string) (lopper.Worktree, error) {
	gitDir := repo
	if !isGitDir(repo) {
		gitDir = filepath.Join(repo, ".git")
	}
	want := realPath(path)
	var (
		wt    lopper.Worktree
		found bool
	)
	_, err := listRepo(ctx, git, gitDir, func(w lopper.Worktree) {
		if realPath(w.Path) == want {
			wt, found = w, true
		}
	})
	if err != nil {
		return lopper.Worktree{}, err
	}
	if !found {
		return lopper.Worktree{}, ErrNotFound
	}
	present(&wt)
	return wt, nil
}

// present reports whether a listed worktree is still on disk, and marks it
// unconfirmed when git can no longer use its admin directory. git never
// calls a locked worktree prunable, so present checks the .git file as
// backLink does, since someone may have recreated the directory.
func present(wt *lopper.Worktree) bool {
	dotGit := filepath.Join(wt.Path, ".git")
	if wt.State == lopper.StateGone || isGone(dotGit) {
		return false
	}
	if gf, ok := readGitFile(dotGit); ok && gf.damage != "" {
		wt.State, wt.Reason = lopper.StateUnconfirmed, gf.damage
	}
	return true
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return line
}

// orphan is a linked worktree found through its .git file, with what that
// file says about its repository.
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
	_, err := os.Lstat(path) //nolint:gosec // G703: paths git wrote, see readGitFile
	return errors.Is(err, fs.ErrNotExist)
}
