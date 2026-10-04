// Package discovery finds git repositories on disk and lists their
// linked worktrees. It decides *where* worktrees are, never whether
// they are safe to delete.
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
	Roots   []string // walked in full: no directory below them is skipped
	Listers int      // concurrent `git worktree list` processes; at least 1
	// Known are places where an earlier scan met repositories, as Met
	// receives them. Those below Roots are looked at before the walk, so
	// that their repositories are listed first. Which worktrees a scan
	// finds does not depend on them, only when.
	Known []string
	// KnownListed, if set, is called once every repository met at Known
	// has been listed and its worktrees emitted, before Scan returns.
	KnownListed func()
	// Met, if set, receives the places where a scan met repositories, for
	// a later scan to take as Known. It is called only when the walk was
	// over, neither failed nor cancelled.
	Met func(places []string)
}

// Scan walks opts.Roots and calls emit for every linked worktree found,
// including orphaned ones that no repository tracks anymore, moved ones
// at the path they were moved to, and unconfirmed ones, whose .git file
// names a repository that did not list them, or could not be listed.
// emit may be called concurrently. A missing or unreadable root is an
// error; unreadable directories below a root are skipped.
func Scan(ctx context.Context, git gitx.Runner, opts Options, emit func(lopper.Worktree)) error {
	type repo struct {
		gitDir string
		known  bool // met at opts.Known
	}
	gitDirs := make(chan repo, 64)
	var (
		listed sync.Map       // real paths of worktrees reported by their repository
		known  sync.WaitGroup // repositories met at opts.Known, until listed
	)

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
			for r := range gitDirs {
				gitDir := r.gitDir
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
				if r.known {
					known.Done()
				}
			}
		})
	}

	var knownDone sync.WaitGroup
	met, err := findRepos(ctx, opts,
		func(gitDir string, isKnown bool) {
			if isKnown {
				known.Add(1)
			}
			gitDirs <- repo{gitDir, isKnown}
		},
		func(dir string, gf gitFile) {
			mu.Lock()
			defer mu.Unlock()
			if gf.orphaned {
				wt := orphanWorktree(dir, gf.commonDir)
				orphans[realPath(wt.Path)] = orphan{wt, gf}
				return
			}
			candidates[realPath(dir)] = orphan{lopper.Worktree{Path: filepath.Clean(dir)}, gf}
		},
		func() {
			// Every known repository has been queued by now; the walk
			// queues the rest behind them.
			knownDone.Go(func() {
				known.Wait()
				if opts.KnownListed != nil {
					opts.KnownListed()
				}
			})
		})
	close(gitDirs)
	wg.Wait()
	knownDone.Wait()
	if err == nil && ctx.Err() == nil && opts.Met != nil {
		opts.Met(met)
	}

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
		// Found through its .git file, which the repository did not
		// confirm: reported rather than lost when git fails.
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

// ErrNotFound is returned by Find and Lookup for a path where they find
// no linked worktree: the main worktree, a submodule or a plain directory
// is never one.
var ErrNotFound = errors.New("not a linked worktree")

// Find returns the linked worktree at path as a Scan of opts.Roots
// reports it: moved, orphaned or unconfirmed alike. The roots must reach
// path; when its directory is gone, only a walk that meets its repository
// finds it.
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

// Lookup finds the linked worktree at path as the repository at repo, a
// lopper.Repo path, lists it now, marked as Scan marks it. Unlike Find, it
// needs no walk: this is how a worktree whose directory is gone is looked
// up again.
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

// present reports whether a worktree its repository lists is still on
// disk, and marks it unconfirmed when git lists it but can no longer use
// its admin directory. git does not call a locked worktree prunable, so
// its .git file is checked, as backLink does: the directory may have been
// recreated.
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

// orphan is a linked worktree found through its .git file, and what that
// file tells about its repository.
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
