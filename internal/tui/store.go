package tui

import (
	"github.com/shinokamix/lopper/internal/engine"
	"github.com/shinokamix/lopper/internal/lopper"
)

// row is everything the engine has reported about one worktree.
type row struct {
	worktree lopper.Worktree
	facts    lopper.Facts
	verdict  lopper.Verdict
}

// store is the single source of truth for scan results: every screen
// reads worktrees from it, and only engine events mutate it. UI state
// such as the cursor and the selection lives in the screens.
type store struct {
	order    []lopper.ID
	byID     map[lopper.ID]*row
	scanning bool
	err      error
}

func newStore() *store {
	return &store{byID: map[lopper.ID]*row{}, scanning: true}
}

// apply folds an engine event into the store.
func (s *store) apply(ev engine.Event) {
	switch ev := ev.(type) {
	case engine.WorktreeFound:
		if _, ok := s.byID[ev.Worktree.ID]; !ok {
			s.order = append(s.order, ev.Worktree.ID)
			s.byID[ev.Worktree.ID] = &row{worktree: ev.Worktree}
		}
	case engine.FactsUpdated:
		if r, ok := s.byID[ev.ID]; ok {
			r.facts, r.verdict = ev.Facts, ev.Verdict
		}
	case engine.ScanDone:
		s.scanning, s.err = false, ev.Err
	}
}

// rows returns rows in discovery order. TODO: sorting and filtering.
func (s *store) rows() []*row {
	out := make([]*row, len(s.order))
	for i, id := range s.order {
		out[i] = s.byID[id]
	}
	return out
}

// summary counts rows per level and the bytes reclaimable from safe ones.
func (s *store) summary() (counts map[lopper.Level]int, safeBytes int64) {
	counts = map[lopper.Level]int{}
	for _, r := range s.byID {
		counts[r.verdict.Level]++
		if r.verdict.Level == lopper.LevelSafe && r.facts.SizeBytes != nil {
			safeBytes += *r.facts.SizeBytes
		}
	}
	return counts, safeBytes
}
