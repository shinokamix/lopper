// Package engine scans for worktrees and removes them.
package engine

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"github.com/shinokamix/lopper/apps/lopper/internal/discovery"
	"github.com/shinokamix/lopper/apps/lopper/internal/gitx"
	"github.com/shinokamix/lopper/apps/lopper/internal/inspect"
	"github.com/shinokamix/lopper/apps/lopper/internal/lopper"
	"github.com/shinokamix/lopper/apps/lopper/internal/pathx"
	"github.com/shinokamix/lopper/apps/lopper/internal/verdict"
)

// Event is a sealed sum type.
//
//sumtype:decl
type Event interface{ isEvent() }

// WorktreeFound is sent when discovery finds a worktree.
type WorktreeFound struct{ Worktree lopper.Worktree }

// FactsUpdated is sent whenever more facts about a worktree are known. Final
// marks the last facts inspection gives. One more Final update follows it if
// the scan finds another repository inside this one afterwards.
type FactsUpdated struct {
	ID      lopper.ID
	Facts   lopper.Facts
	Verdict lopper.Verdict
	Final   bool
}

// ScanDone is the last event of a scan.
type ScanDone struct{ Err error }

func (WorktreeFound) isEvent() {}
func (FactsUpdated) isEvent()  {}
func (ScanDone) isEvent()      {}

// Options control a scan.
type Options struct {
	Roots []string
	// Concurrency caps the git processes a scan runs at once. Less than 1
	// means runtime.GOMAXPROCS.
	Concurrency int
	// Known are places where an earlier scan met repositories. The scan
	// visits them first and finds the same worktrees without them.
	Known []string
	// Remember, if set, receives the places where a complete scan met
	// repositories, for a later scan's Known.
	Remember func(places []string)
}

func budget(n int) (listers, workers int) {
	if n <= 0 {
		n = runtime.GOMAXPROCS(0)
	}
	listers = max(n/4, 1)
	workers = max(n-listers, 1)
	return listers, workers
}

// Engine runs scans of worktrees and removes them.
type Engine struct {
	git gitx.Runner
}

// New returns an Engine that runs the git binary in PATH.
func New() *Engine {
	return &Engine{git: gitx.Exec{}}
}

// Check reports whether the tools a scan depends on are available.
func (e *Engine) Check(ctx context.Context) error {
	return gitx.Check(ctx)
}

// Scan starts a scan and returns its events. A worktree's WorktreeFound
// comes before its FactsUpdated events. ScanDone comes last, then the
// channel closes. Cancelling ctx stops the scan early. The channel still
// closes, but ScanDone may be dropped if nobody reads.
func (e *Engine) Scan(ctx context.Context, opts Options) <-chan Event {
	events := make(chan Event, 256)
	found := make(chan lopper.Worktree, 256)
	insp := inspect.Inspector{Git: e.git}
	listers, n := budget(opts.Concurrency)
	var (
		repos     repoDirs
		mu        sync.Mutex
		inspected []inspection
	)

	var workers sync.WaitGroup
	for range n {
		workers.Go(func() {
			for wt := range found {
				f := insp.Quick(ctx, wt)
				f.NestedRepos = repos.inside(wt)
				send[Event](ctx, events, FactsUpdated{ID: wt.ID, Facts: f, Verdict: verdict.Of(wt, f)})
				f = insp.Slow(ctx, wt, f)
				f.NestedRepos = repos.inside(wt)
				send[Event](ctx, events, FactsUpdated{ID: wt.ID, Facts: f, Verdict: verdict.Of(wt, f), Final: true})
				mu.Lock()
				inspected = append(inspected, inspection{wt, f})
				mu.Unlock()
			}
		})
	}

	go func() {
		dopts := discovery.Options{
			Roots:   opts.Roots,
			Listers: listers,
			Known:   opts.Known,
			Met:     opts.Remember,
			Repos:   repos.add,
		}
		err := discovery.Scan(ctx, e.git, dopts, func(wt lopper.Worktree) {
			if send[Event](ctx, events, WorktreeFound{Worktree: wt}) {
				send(ctx, found, wt)
			}
		})
		close(found)
		workers.Wait()
		for _, in := range inspected {
			if n := repos.inside(in.wt); n != in.facts.NestedRepos {
				in.facts.NestedRepos = n
				send[Event](ctx, events, FactsUpdated{ID: in.wt.ID, Facts: in.facts, Verdict: verdict.Of(in.wt, in.facts), Final: true})
			}
		}
		done := ScanDone{Err: err}
		select {
		case events <- done:
		default:
			send[Event](ctx, events, done)
		}
		close(events)
	}()

	return events
}

// NotSafeError is returned by Remove, unless forced, for a worktree that
// is not safe to delete.
type NotSafeError struct{ Verdict lopper.Verdict }

func (e *NotSafeError) Error() string {
	why := e.Verdict.Reasons()
	for _, n := range e.Verdict.Notes {
		if n.Hint != "" {
			why = append(why, n.Hint)
		}
	}
	return "not safe to delete: " + strings.Join(why, " · ")
}

// RecordLeftError is returned by Remove when it deleted a worktree's
// directory but git kept its record.
type RecordLeftError struct{ Repo string }

func (e *RecordLeftError) Error() string {
	return "git's record of it is left in " + e.Repo + ": `git worktree prune` there removes it"
}

type inspection struct {
	wt    lopper.Worktree
	facts lopper.Facts
}

type repoDirs struct {
	mu   sync.Mutex
	dirs map[string]struct{}
}

func (r *repoDirs) add(dir string) {
	dir = physical(dir)
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.dirs == nil {
		r.dirs = map[string]struct{}{}
	}
	r.dirs[dir] = struct{}{}
}

func (r *repoDirs) inside(wt lopper.Worktree) int {
	if wt.State == lopper.StateGone {
		return 0
	}
	root := physical(wt.Path)
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for dir := range r.dirs {
		if rel, ok := pathx.Within(root, dir); ok && rel != "." {
			n++
		}
	}
	return n
}

// Find returns the linked worktree at path, uninspected, as a scan of roots
// reports it, ready for Remove. The roots must reach path, and its
// repository too if the directory is gone.
func (e *Engine) Find(ctx context.Context, roots []string, path string) (lopper.Worktree, error) {
	wt, _, err := e.find(ctx, roots, path)
	return wt, err
}

func (e *Engine) find(ctx context.Context, roots []string, path string) (wt lopper.Worktree, nestedRepos int, err error) {
	listers, _ := budget(0)
	var repos repoDirs
	opts := discovery.Options{Roots: roots, Listers: listers, Repos: repos.add}
	wt, err = discovery.Find(ctx, e.git, opts, path)
	if err != nil {
		return lopper.Worktree{}, 0, err
	}
	return wt, repos.inside(wt), nil
}

// Remove deletes a worktree lopper found, in any state, with its directory
// and git's record of it, and keeps the branch. It looks the worktree up
// again first, since it may have changed. Unless forced, Remove inspects it
// and leaves it if it is not safe, and git itself refuses if it gained
// uncommitted work by then. Forced, it goes with whatever it holds. Remove
// never deletes the directory that contains the current directory.
func (e *Engine) Remove(ctx context.Context, wt lopper.Worktree, force bool) error {
	now, nestedRepos, err := e.current(ctx, wt)
	if err != nil {
		return err
	}
	if inside(now.Path) {
		return errors.New("the current directory is inside it")
	}
	if !force {
		f := (inspect.Inspector{Git: e.git}).Quick(ctx, now)
		f.NestedRepos = nestedRepos
		if v := verdict.Of(now, f); !v.Safe {
			return &NotSafeError{Verdict: v}
		}
	}
	switch now.State {
	case lopper.StateTracked, lopper.StateGone:
	case lopper.StateOrphaned:
		return os.RemoveAll(now.Path)
	case lopper.StateUnconfirmed:
		if err := os.RemoveAll(now.Path); err != nil {
			return err
		}
		if gitx.RemoveWorktree(ctx, e.git, now.Repo.Path, now.Path, true) != nil {
			return &RecordLeftError{Repo: now.Repo.Path}
		}
		return nil
	case lopper.StateMoved:
		if err := gitx.RepairWorktree(ctx, e.git, now.Path); err != nil {
			return err
		}
	}
	return gitx.RemoveWorktree(ctx, e.git, now.Repo.Path, now.Path, force)
}

func (e *Engine) current(ctx context.Context, wt lopper.Worktree) (now lopper.Worktree, nestedRepos int, err error) {
	if _, err := os.Lstat(wt.Path); errors.Is(err, fs.ErrNotExist) {
		if wt.Repo.Path == "" {
			return lopper.Worktree{}, 0, discovery.ErrNotFound
		}
		now, err := discovery.Lookup(ctx, e.git, wt.Repo.Path, wt.Path)
		return now, 0, err
	}
	return e.find(ctx, []string{wt.Path}, wt.Path)
}

// Windows does not allow removing the directory a process runs in.
func inside(dir string) bool {
	cwd, err := os.Getwd()
	if err != nil {
		return false
	}
	_, ok := pathx.Within(physical(dir), physical(cwd))
	return ok
}

func physical(path string) string {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return resolved
	}
	return path
}

func send[T any](ctx context.Context, ch chan<- T, v T) bool {
	select {
	case ch <- v:
		return true
	case <-ctx.Done():
		return false
	}
}
