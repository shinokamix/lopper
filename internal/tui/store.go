package tui

import (
	"cmp"
	"slices"

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
	listed   bool // the repositories an earlier scan met are listed
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
	case engine.KnownListed:
		s.listed = true
	case engine.ScanDone:
		s.scanning, s.err = false, ev.Err
	}
}

// group is the worktrees of one repository.
type group struct {
	repo  lopper.Repo
	rows  []*row
	found int // worktrees found in the repository, shown or not
}

// groups returns rows grouped by repository in the order they were
// found: a row, once shown, keeps its place as facts arrive, and new
// rows and repositories are added at the end.
func (s *store) groups() []group {
	if s.grouped == nil {
		s.grouped = s.group()
	}
	return s.grouped
}

func (s *store) group() []group {
	byRepo := map[string]int{}
	var out []group
	for _, id := range s.order {
		r := s.byID[id]
		i, ok := byRepo[r.worktree.Repo.Path]
		if !ok {
			i = len(out)
			byRepo[r.worktree.Repo.Path] = i
			out = append(out, group{repo: r.worktree.Repo})
		}
		out[i].rows = append(out[i].rows, r)
		out[i].found++
	}
	return out
}

// ranks is a display order: the place of each worktree in its group and
// of each group, by repository path. Those ranked later go last.
type ranks struct {
	rows  map[lopper.ID]int
	repos map[string]int
}

// sizeRanks ranks groups largest first: groups by the total of their
// known sizes and rows within a group by their own, so what frees the
// most space is on top. Rows still being measured go after the measured
// ones; ties keep the order found.
func sizeRanks(groups []group) ranks {
	rk := ranks{rows: map[lopper.ID]int{}, repos: map[string]int{}}
	totals := map[string]int64{}
	measured := func(r *row) int64 { // -1 while unknown, after every size
		if r.facts.SizeBytes == nil {
			return -1
		}
		return *r.facts.SizeBytes
	}
	for _, g := range groups {
		rows := slices.Clone(g.rows)
		slices.SortStableFunc(rows, func(a, b *row) int { return cmp.Compare(measured(b), measured(a)) })
		for i, r := range rows {
			rk.rows[r.worktree.ID] = i
			totals[g.repo.Path] += sizeOf(r)
		}
	}
	byTotal := slices.Clone(groups)
	slices.SortStableFunc(byTotal, func(a, b group) int {
		return cmp.Compare(totals[b.repo.Path], totals[a.repo.Path])
	})
	for i, g := range byTotal {
		rk.repos[g.repo.Path] = i
	}
	return rk
}

// sort orders groups and their rows by rk in place; those it does not
// rank, found since, go last in the order found.
func (rk ranks) sort(groups []group) {
	for _, g := range groups {
		slices.SortStableFunc(g.rows, func(a, b *row) int {
			return cmp.Compare(rankOf(rk.rows, a.worktree.ID), rankOf(rk.rows, b.worktree.ID))
		})
	}
	slices.SortStableFunc(groups, func(a, b group) int {
		return cmp.Compare(rankOf(rk.repos, a.repo.Path), rankOf(rk.repos, b.repo.Path))
	})
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

// rankOf is the rank of k in m, after all of them when it has none.
func rankOf[K comparable](m map[K]int, k K) int {
	if i, ok := m[k]; ok {
		return i
	}
	return len(m)
}
