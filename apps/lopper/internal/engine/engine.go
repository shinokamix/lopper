// Package engine runs the scan pipeline. Discovery finds worktrees, a pool
// of inspect workers gathers their quick and then slow facts, and both turn
// into events. The TUI and the CLI read the events and never call the
// stages. They remove worktrees through [Engine.Remove].
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
	"github.com/shinokamix/lopper/apps/lopper/internal/verdict"
)

// Event is a sealed sum type; gochecksumtype verifies type switches over it.
//
//sumtype:decl
type Event interface{ isEvent() }

// WorktreeFound is sent when discovery finds a worktree.
type WorktreeFound struct{ Worktree lopper.Worktree }

// FactsUpdated is sent whenever more facts about a worktree are known.
type FactsUpdated struct {
	ID    lopper.ID
	Facts lopper.Facts
	Safe  bool
	Final bool // the last update for this worktree
}

// ScanDone is the last event of a scan.
type ScanDone struct{ Err error }

func (WorktreeFound) isEvent() {}
func (FactsUpdated) isEvent()  {}
func (ScanDone) isEvent()      {}

// Options control a scan.
type Options struct {
	Roots []string
	// Concurrency caps the git processes a scan runs at once;
	// 0 means runtime.GOMAXPROCS(0). See [budget].
	Concurrency int
	// Known are places where an earlier scan met repositories. The scan finds
	// their worktrees first, and finds the same ones without them.
	Known []string
	// Remember, if set, receives the places where a complete scan met
	// repositories, for a later scan's Known.
	Remember func(places []string)
}

// budget splits the git process limit n between discovery listers and
// inspect workers. git is CPU- and disk-bound, so more processes than CPUs
// only add contention. Inspection does more work per repository than
// listing, so it gets the larger share. Both get at least one, so the total
// is max(n, 2).
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

	var workers sync.WaitGroup
	for range n {
		workers.Go(func() {
			for wt := range found {
				f := insp.Quick(ctx, wt)
				send[Event](ctx, events, FactsUpdated{ID: wt.ID, Facts: f, Safe: verdict.Safe(wt, f)})
				f = insp.Slow(ctx, wt, f)
				send[Event](ctx, events, FactsUpdated{ID: wt.ID, Facts: f, Safe: verdict.Safe(wt, f), Final: true})
			}
		})
	}

	go func() {
		dopts := discovery.Options{
			Roots:   opts.Roots,
			Listers: listers,
			Known:   opts.Known,
			Met:     opts.Remember,
		}
		err := discovery.Scan(ctx, e.git, dopts, func(wt lopper.Worktree) {
			// Inspect only what the consumer was told about, or it would
			// receive facts for an unknown worktree.
			if send[Event](ctx, events, WorktreeFound{Worktree: wt}) {
				send(ctx, found, wt)
			}
		})
		close(found)
		workers.Wait()
		done := ScanDone{Err: err}
		select {
		case events <- done: // deliver while there is room, even if cancelled
		default:
			send[Event](ctx, events, done)
		}
		close(events)
	}()

	return events
}

// NotSafeError is returned by Remove, unless forced, for a worktree that
// is not safe to delete.
type NotSafeError struct {
	Worktree lopper.Worktree
	Facts    lopper.Facts
}

func (e *NotSafeError) Error() string {
	var why []string
	for _, n := range verdict.Notes(e.Worktree, e.Facts) {
		why = append(why, n.Text)
	}
	if e.Facts.UncheckedFiles != nil && *e.Facts.UncheckedFiles > 0 {
		why = append(why, "index flags hide their edits from git: `git ls-files -v | grep '^[a-zS]'` lists them, "+
			"`git update-index --no-assume-unchanged -- <file>` and `git update-index --no-skip-worktree -- <file>` "+
			"clear them for `git status`")
	}
	return "not safe to delete: " + strings.Join(why, " · ")
}

// RecordLeftError is returned by Remove when a worktree's directory is gone
// but git could not remove its record, because git cannot tell which record
// is the worktree's.
type RecordLeftError struct{ Repo string }

func (e *RecordLeftError) Error() string {
	return "git's record of it is left in " + e.Repo + ": `git worktree prune` there removes it"
}

// Find returns the linked worktree at path, uninspected, as a scan of roots
// reports it, ready for Remove. The roots must reach path, and its
// repository too if the directory is gone.
func (e *Engine) Find(ctx context.Context, roots []string, path string) (lopper.Worktree, error) {
	listers, _ := budget(0)
	return discovery.Find(ctx, e.git, discovery.Options{Roots: roots, Listers: listers}, path)
}

// Remove deletes a worktree lopper found, in any state, with its directory
// and git's record of it, and keeps the branch. It looks the worktree up
// again first, since it may have changed. Unless forced, Remove inspects it
// and leaves it if it is not safe, and git itself refuses if it gained
// uncommitted work by then. Forced, it goes with whatever it holds. Remove
// never deletes the directory that contains the current directory.
func (e *Engine) Remove(ctx context.Context, wt lopper.Worktree, force bool) error {
	now, err := e.current(ctx, wt)
	if err != nil {
		return err
	}
	if inside(now.Path) {
		return errors.New("the current directory is inside it")
	}
	if !force {
		if f := (inspect.Inspector{Git: e.git}).Quick(ctx, now); !verdict.Safe(now, f) {
			return &NotSafeError{Worktree: now, Facts: f}
		}
	}
	switch now.State {
	case lopper.StateTracked, lopper.StateGone:
	case lopper.StateOrphaned:
		// No repository tracks it. Its record is gone or belongs to another
		// worktree now.
		return os.RemoveAll(now.Path)
	case lopper.StateUnconfirmed:
		// git cannot work in it, but once it is gone, git can remove a
		// record that still names it.
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

// Measure returns the size of a worktree's directory as a scan measures it,
// or nil if it cannot. A removal uses it to report the space freed when the
// scan has not measured the worktree yet.
func (e *Engine) Measure(ctx context.Context, wt lopper.Worktree) *int64 {
	return inspect.Inspector{Git: e.git}.Slow(ctx, wt, lopper.Facts{}).SizeBytes
}

// current looks wt up again, by a walk of its directory or, if that is
// gone, in its repository.
func (e *Engine) current(ctx context.Context, wt lopper.Worktree) (lopper.Worktree, error) {
	if _, err := os.Lstat(wt.Path); errors.Is(err, fs.ErrNotExist) {
		if wt.Repo.Path == "" {
			return lopper.Worktree{}, discovery.ErrNotFound
		}
		return discovery.Lookup(ctx, e.git, wt.Repo.Path, wt.Path)
	}
	return e.Find(ctx, []string{wt.Path}, wt.Path)
}

// inside reports whether the current directory is dir or below it.
// Removing dir would delete the directory under the process, which Windows
// does not allow.
func inside(dir string) bool {
	cwd, err := os.Getwd()
	if err != nil {
		return false
	}
	resolve := func(p string) string {
		if r, err := filepath.EvalSymlinks(p); err == nil {
			return r
		}
		return p
	}
	rel, err := filepath.Rel(resolve(dir), resolve(cwd))
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// send delivers v unless ctx is cancelled, so an abandoned consumer never
// leaves pipeline goroutines blocked. It reports whether v was delivered.
func send[T any](ctx context.Context, ch chan<- T, v T) bool {
	select {
	case ch <- v:
		return true
	case <-ctx.Done():
		return false
	}
}
