// Package engine orchestrates the scan pipeline:
//
//	discovery → worktrees → inspect pool (quick, then slow) → events
//
// UIs (TUI, CLI) consume the event stream and never call the stages directly.
package engine

import (
	"context"
	"runtime"
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
	ID      lopper.ID
	Facts   lopper.Facts
	Verdict lopper.Verdict
	Final   bool // no more updates will follow for this worktree
}

// ScanDone is the last event of a scan.
type ScanDone struct{ Err error }

func (WorktreeFound) isEvent() {}
func (FactsUpdated) isEvent()  {}
func (ScanDone) isEvent()      {}

// Options control a scan.
type Options struct {
	Roots     []string
	SkipNames map[string]bool // directory base names never descended into, at any depth
	SkipPaths map[string]bool // absolute directories never descended into
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
				send[Event](ctx, events, FactsUpdated{ID: wt.ID, Facts: f, Verdict: verdict.Evaluate(wt, f, verdict.DefaultRules)})
				f = insp.Slow(ctx, wt, f)
				send[Event](ctx, events, FactsUpdated{ID: wt.ID, Facts: f, Verdict: verdict.Evaluate(wt, f, verdict.DefaultRules), Final: true})
			}
		})
	}

	go func() {
		dopts := discovery.Options{
			Roots:     opts.Roots,
			SkipNames: opts.SkipNames,
			SkipPaths: opts.SkipPaths,
			Listers:   listers,
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
