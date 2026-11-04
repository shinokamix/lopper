package tui

import (
	"cmp"
	"math"
	"slices"

	"github.com/shinokamix/lopper/apps/lopper/internal/engine"
	"github.com/shinokamix/lopper/apps/lopper/internal/lopper"
)

type row struct {
	worktree lopper.Worktree
	facts    lopper.Facts
	checked  bool
	final    bool
	verdict  lopper.Verdict
}

type store struct {
	order    []lopper.ID
	byID     map[lopper.ID]*row
	scanning bool
	err      error
	grouped  []group
}

func newStore() *store {
	return &store{byID: map[lopper.ID]*row{}, scanning: true}
}

func (s *store) remove(id lopper.ID) {
	s.grouped = nil
	delete(s.byID, id)
	s.order = slices.DeleteFunc(s.order, func(o lopper.ID) bool { return o == id })
}

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
			r.facts, r.verdict, r.checked, r.final = ev.Facts, ev.Verdict, true, ev.Final
		}
	case engine.ScanDone:
		s.scanning, s.err = false, ev.Err
	}
}

type group struct {
	repo  lopper.Repo
	rows  []*row
	found int
}

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

type ranks struct {
	rows  map[lopper.ID]int
	repos map[string]int
}

func sizeRanks(groups []group) ranks {
	measured := func(r *row) int64 {
		if r.facts.SizeBytes == nil {
			return -1
		}
		return *r.facts.SizeBytes
	}
	total := func(rows []*row) int64 {
		var sum int64
		for _, r := range rows {
			sum += sizeOf(r)
		}
		return sum
	}
	return rankBy(groups, measured, total)
}

func ageRanks(groups []group) ranks {
	staleness := func(r *row) int64 {
		if r.facts.LastCommit == nil {
			return math.MinInt64
		}
		return -r.facts.LastCommit.Unix()
	}
	oldest := func(rows []*row) int64 {
		most := int64(math.MinInt64)
		for _, r := range rows {
			most = max(most, staleness(r))
		}
		return most
	}
	return rankBy(groups, staleness, oldest)
}

func rankBy(groups []group, rowKey func(*row) int64, repoKey func([]*row) int64) ranks {
	rk := ranks{rows: map[lopper.ID]int{}, repos: map[string]int{}}
	keys := map[string]int64{}
	for _, g := range groups {
		rows := slices.Clone(g.rows)
		slices.SortStableFunc(rows, func(a, b *row) int { return cmp.Compare(rowKey(b), rowKey(a)) })
		for i, r := range rows {
			rk.rows[r.worktree.ID] = i
		}
		keys[g.repo.Path] = repoKey(g.rows)
	}
	byKey := slices.Clone(groups)
	slices.SortStableFunc(byKey, func(a, b group) int {
		return cmp.Compare(keys[b.repo.Path], keys[a.repo.Path])
	})
	for i, g := range byKey {
		rk.repos[g.repo.Path] = i
	}
	return rk
}

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

func sizeOf(r *row) int64 {
	if r.facts.SizeBytes == nil {
		return 0
	}
	return *r.facts.SizeBytes
}

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

func rankOf[K comparable](m map[K]int, k K) int {
	if i, ok := m[k]; ok {
		return i
	}
	return len(m)
}
