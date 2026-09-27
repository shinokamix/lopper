package tui

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/shinokamix/lopper/internal/lopper"
)

// list is the worktree list screen. It reads rows from the store and
// owns its UI state: the cursor and the selection.
type list struct {
	cursor   int
	selected map[lopper.ID]bool
}

func newList() list {
	return list{selected: map[lopper.ID]bool{}}
}

func (l *list) update(msg tea.KeyPressMsg, k keyMap, s *store) {
	rows := s.rows()
	switch {
	case key.Matches(msg, k.up):
		l.cursor = max(l.cursor-1, 0)
	case key.Matches(msg, k.down):
		l.cursor = min(l.cursor+1, max(len(rows)-1, 0))
	case key.Matches(msg, k.toggle):
		if l.cursor < len(rows) {
			id := rows[l.cursor].worktree.ID
			if l.selected[id] {
				delete(l.selected, id)
			} else {
				l.selected[id] = true
			}
		}
	}
}

func (l *list) view(t theme, s *store, width, height int) string {
	var b strings.Builder

	counts, safeBytes := s.summary()
	status := "done"
	if s.scanning {
		status = "scanning…"
	} else if s.err != nil {
		status = "failed: " + s.err.Error()
	}
	fmt.Fprintf(&b, "%s  %s\n", t.title.Render("lopper"), t.subtle.Render(status))
	fmt.Fprintf(&b, "%s  %s  %s  %s\n\n",
		t.level[lopper.LevelSafe].Render(fmt.Sprintf("%d safe", counts[lopper.LevelSafe])),
		t.level[lopper.LevelReview].Render(fmt.Sprintf("%d review", counts[lopper.LevelReview])),
		t.level[lopper.LevelKeep].Render(fmt.Sprintf("%d keep", counts[lopper.LevelKeep])),
		t.subtle.Render("reclaimable "+formatBytes(&safeBytes)),
	)

	rows := s.rows()
	if len(rows) == 0 {
		b.WriteString(t.subtle.Render("  no linked worktrees found yet"))
		return b.String()
	}

	// TODO: detail pane, grouping by repo.
	visible := max(height-6, 1)
	start := max(0, l.cursor-visible+1)
	for i, r := range rows[start:min(len(rows), start+visible)] {
		mark := "  "
		if l.selected[r.worktree.ID] {
			mark = t.selected.Render("✓ ")
		}
		name := r.worktree.Branch
		if name == "" {
			name = "(detached)"
		}
		line := fmt.Sprintf("%s%s %-32.32s %9s  %s", mark, badge(t, r.verdict.Level),
			name, formatBytes(r.facts.SizeBytes), t.subtle.Render(r.worktree.Path))
		if start+i == l.cursor {
			line = t.cursor.Width(width).Render(line)
		}
		b.WriteString(line + "\n")
	}
	return b.String()
}
