package tui

import (
	"cmp"
	"slices"
	"strings"

	"github.com/shinokamix/lopper/internal/engine"
	"github.com/shinokamix/lopper/internal/lopper"
)

// row is everything the engine has reported about one worktree.
type row struct {
	worktree lopper.Worktree
	facts    lopper.Facts
	checked  bool // facts have arrived; until then none are known
	final    bool // no more facts will arrive
	safe     bool
}

// store is the single source of truth for scan results: every screen
// reads worktrees from it, and only engine events and removals mutate
// it. UI state such as the cursor and the selection lives in the screens.
type store struct {
	order    []lopper.ID
	byID     map[lopper.ID]*row
	scanning bool
	err      error
	grouped  []group // groups() until the next event; nil when stale
}

func newStore() *store {
	return &store{byID: map[lopper.ID]*row{}, scanning: true}
}

// remove forgets a worktree that was removed from disk.
func (s *store) remove(id lopper.ID) {
	s.grouped = nil
	delete(s.byID, id)
	s.order = slices.DeleteFunc(s.order, func(o lopper.ID) bool { return o == id })
}

// apply folds an engine event into the store.
func (s *store) apply(ev engine.Event) {
	s.grouped = nil
	switch ev := ev.(type) {
	case engine.WorktreeFound:
		if _, ok := s.byID[ev.Worktree.ID]; !ok {
			s.order = append(s.order, ev.Worktree.ID)
			s.byID[ev.Worktree.ID] = &row{worktree: ev.Worktree}
		}
	case engine.FactsUpdated:
		if r, ok := s.byID[ev.ID]; ok {
			r.facts, r.safe, r.checked, r.final = ev.Facts, ev.Safe, true, ev.Final
		}
	case engine.ScanDone:
		s.scanning, s.err = false, ev.Err
	}
}

// group is the worktrees of one repository.
type group struct {
	repo lopper.Repo
	rows []*row
}

// groups returns rows grouped by repository, largest first: groups by
// their total size and rows within a group by their own, so what frees
// the most space is on top. Sizes still being measured count as zero;
// names break ties, keeping the order stable until sizes arrive.
// TODO: filtering, other sort orders.
func (s *store) groups() []group {
	if s.grouped == nil {
		s.grouped = s.group()
	}
	return s.grouped
}

func (s *store) group() []group {
	byRepo := map[string]*group{}
	var out []*group
	for _, id := range s.order {
		r := s.byID[id]
		g, ok := byRepo[r.worktree.Repo.Path]
		if !ok {
			g = &group{repo: r.worktree.Repo}
			byRepo[r.worktree.Repo.Path] = g
			out = append(out, g)
		}
		g.rows = append(g.rows, r)
	}
	for _, g := range out {
		slices.SortFunc(g.rows, func(a, b *row) int {
			return cmp.Or(
				cmp.Compare(sizeOf(b), sizeOf(a)),
				cmp.Compare(branchName(a.worktree), branchName(b.worktree)),
				cmp.Compare(a.worktree.Path, b.worktree.Path))
		})
	}
	total := func(g *group) int64 {
		var n int64
		for _, r := range g.rows {
			n += sizeOf(r)
		}
		return n
	}
	slices.SortFunc(out, func(a, b *group) int {
		return cmp.Or(
			cmp.Compare(total(b), total(a)),
			cmp.Compare(strings.ToLower(repoName(a.repo.Path)), strings.ToLower(repoName(b.repo.Path))),
			cmp.Compare(a.repo.Path, b.repo.Path))
	})
	groups := make([]group, len(out))
	for i, g := range out {
		groups[i] = *g
	}
	return groups
}

// sizeOf is a row's size, zero while unknown.
func sizeOf(r *row) int64 {
	if r.facts.SizeBytes == nil {
		return 0
	}
	return *r.facts.SizeBytes
}

// sizeText is the total size of rows as shown: spin, the spinner's
// frame, while any is still being measured, and "?" when one could not
// be, as a partial total would understate it.
func sizeText(rows []*row, spin string) string {
	var total int64
	unknown := false
	for _, r := range rows {
		switch {
		case r.facts.SizeBytes != nil:
			total += *r.facts.SizeBytes
		case !r.final:
			return spin
		default:
			unknown = true
		}
	}
	if unknown {
		return "?"
	}
	return formatBytes(total)
}

// totalSize sums the sizes of rows, or returns nil while any is unknown:
// a partial total would understate what deleting them frees.
func totalSize(rows []*row) *int64 {
	total := new(int64)
	for _, r := range rows {
		if r.facts.SizeBytes == nil {
			return nil
		}
		*total += *r.facts.SizeBytes
	}
	return total
}
