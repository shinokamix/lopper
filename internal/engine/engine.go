// Package engine orchestrates the scan pipeline:
//
//	discovery → worktrees → inspect pool (quick, then slow) → events
//
// UIs (TUI, CLI) consume the event stream and never call the stages
// directly; they remove worktrees through [Engine.Remove] too.
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

	"github.com/shinokamix/lopper/internal/discovery"
	"github.com/shinokamix/lopper/internal/gitx"
	"github.com/shinokamix/lopper/internal/inspect"
	"github.com/shinokamix/lopper/internal/lopper"
	"github.com/shinokamix/lopper/internal/verdict"
)

// Event is a sealed sum type; gochecksumtype verifies type switches over it.
//
//sumtype:decl
type Event interface{ isEvent() }

// WorktreeFound is sent as soon as a worktree is discovered.
type WorktreeFound struct{ Worktree lopper.Worktree }

// FactsUpdated is sent every time more facts about a worktree are known.
type FactsUpdated struct {
	ID    lopper.ID
	Facts lopper.Facts
	Safe  bool // see [verdict.Safe]
	Final bool // no more updates will follow for this worktree
}

// KnownListed is sent once, when the repositories of Options.Known have
// been listed: the worktrees found after it are in repositories the
// earlier scan did not meet, or not listed by git.
type KnownListed struct{}

// ScanDone is the last event of a scan.
type ScanDone struct{ Err error }

func (WorktreeFound) isEvent() {}
func (FactsUpdated) isEvent()  {}
func (KnownListed) isEvent()   {}
func (ScanDone) isEvent()      {}

// Options control a scan.
type Options struct {
	Roots []string
	// Concurrency caps the git processes a scan runs at once;
	// 0 means runtime.GOMAXPROCS(0). See [budget].
	Concurrency int
	// Known are places where an earlier scan met repositories: their
	// worktrees are found first. A scan finds the same ones without them.
	Known []string
	// Remember, if set, receives the places where a scan met repositories
	// once it is complete, for a later scan to take as Known.
	Remember func(places []string)
}

// budget splits the git process limit n between discovery listers and
// inspect workers. git is CPU- and disk-bound, so running more processes
// than there are CPUs only adds contention. Inspection does far more
// work per repository than listing, so it gets the larger share. Both
// get at least one, so the total is max(n, 2).
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

// Scan starts a scan and returns its event stream. WorktreeFound for a
// worktree always precedes its FactsUpdated events; KnownListed comes
// once, before ScanDone, which is the last event: the channel is closed
// after it. Cancel ctx to stop early: the
// channel is still closed, but ScanDone may be dropped if nobody is
// reading anymore.
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
			KnownListed: func() {
				send[Event](ctx, events, KnownListed{})
			},
			Met: opts.Remember,
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
		case events <- done: // prefer delivery while there is room, even if cancelled
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

// RecordLeftError is returned by Remove when a worktree's directory is
// gone but git's record of it could not be removed: git cannot tell which
// record is the worktree's.
type RecordLeftError struct{ Repo string }

func (e *RecordLeftError) Error() string {
	return "git's record of it is left in " + e.Repo + ": `git worktree prune` there removes it"
}

// Find returns the linked worktree at path as a scan of roots reports it,
// without inspecting it: what Remove takes. The roots must reach path, and
// when its directory is gone, its repository too.
func (e *Engine) Find(ctx context.Context, roots []string, path string) (lopper.Worktree, error) {
	listers, _ := budget(0)
	return discovery.Find(ctx, e.git, discovery.Options{Roots: roots, Listers: listers}, path)
}

// Remove removes a worktree lopper found, whatever kind it is: its
// directory, and git's record of it. The branch is kept. The worktree is
// looked up again first, since it may have changed since it was found.
// Unless forced, it is inspected too and left alone if it is not safe to
// delete, and git itself refuses if it holds uncommitted work by then;
// forced, it goes with whatever it holds. The directory the current
// directory is in always stays.
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
		// No repository tracks it: its record is gone, or belongs to
		// another worktree now.
		return os.RemoveAll(now.Path)
	case lopper.StateUnconfirmed:
		// git cannot work in it, but once it is gone, can remove the
		// record that still names it, if one does.
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

// Measure returns the size of a worktree's directory as a scan measures
// it, or nil when it cannot be measured: for a removal to tell the space
// it freed when the scan had not measured it yet.
func (e *Engine) Measure(ctx context.Context, wt lopper.Worktree) *int64 {
	return inspect.Inspector{Git: e.git}.Slow(ctx, wt, lopper.Facts{}).SizeBytes
}

// current looks wt up again as it is now: by a walk of its directory, or,
// when that is gone, in its repository.
func (e *Engine) current(ctx context.Context, wt lopper.Worktree) (lopper.Worktree, error) {
	if _, err := os.Lstat(wt.Path); errors.Is(err, fs.ErrNotExist) {
		if wt.Repo.Path == "" {
			return lopper.Worktree{}, discovery.ErrNotFound
		}
		return discovery.Lookup(ctx, e.git, wt.Repo.Path, wt.Path)
	}
	return e.Find(ctx, []string{wt.Path}, wt.Path)
}

// inside reports whether the current directory is dir or below it:
// removing it would pull the directory from under the process, which
// Windows does not even allow.
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

// send delivers v unless ctx is cancelled, so an abandoned consumer
// never leaves pipeline goroutines blocked forever. It reports whether
// v was delivered.
func send[T any](ctx context.Context, ch chan<- T, v T) bool {
	select {
	case ch <- v:
		return true
	case <-ctx.Done():
		return false
	}
}
