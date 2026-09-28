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
	"fmt"
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

type Engine struct {
	Git gitx.Runner
}

func New() *Engine {
	return &Engine{Git: gitx.Exec{}}
}

// Check reports whether the tools a scan depends on are available.
func (e *Engine) Check(ctx context.Context) error {
	return gitx.Check(ctx)
}

// Scan starts a scan and returns its event stream. WorktreeFound for a
// worktree always precedes its FactsUpdated events; ScanDone is the last
// event and the channel is closed after it. Cancel ctx to stop early: the
// channel is still closed, but ScanDone may be dropped if nobody is
// reading anymore.
func (e *Engine) Scan(ctx context.Context, opts Options) <-chan Event {
	events := make(chan Event, 256)
	found := make(chan lopper.Worktree, 256)
	insp := inspect.Inspector{Git: e.Git}
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
		}
		err := discovery.Scan(ctx, e.Git, dopts, func(wt lopper.Worktree) {
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
	for _, n := range lopper.Notes(e.Worktree, e.Facts) {
		why = append(why, n.Text)
	}
	return "not safe to delete: " + strings.Join(why, " · ")
}

// Remove removes the worktree at wt.Path through git, which keeps its
// branch. Unless forced, the worktree is inspected again first and left
// alone if it is not safe to delete, and git itself refuses if it holds
// uncommitted work by then; forced, it goes with whatever it holds.
// Either way, only a worktree its repository lists and can confirm at
// this path is removed, and not the one the current directory is in.
// wt.Repo may be empty when the directory still exists.
func (e *Engine) Remove(ctx context.Context, wt lopper.Worktree, force bool) error {
	now, err := discovery.Lookup(ctx, e.Git, wt.Repo.Path, wt.Path)
	if err != nil {
		return err
	}
	if now.Unconfirmed != "" {
		return fmt.Errorf("%s cannot confirm it: %s", now.Repo.Path, now.Unconfirmed)
	}
	if inside(now.Path) {
		return errors.New("the current directory is inside it")
	}
	if !force {
		if f := (inspect.Inspector{Git: e.Git}).Quick(ctx, now); !verdict.Safe(now, f) {
			return &NotSafeError{Worktree: now, Facts: f}
		}
	}
	return gitx.RemoveWorktree(ctx, e.Git, now.Repo.Path, now.Path, force)
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
