package tui

import (
	"fmt"
	"path/filepath"
	"strings"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/shinokamix/lopper/internal/lopper"
)

// list is the worktree list screen. It reads rows from the store and
// owns its UI state: the cursor, the scroll position and the selection.
type list struct {
	// cursor follows a worktree, not a position: rows are sorted, and a
	// worktree found later may land above the cursor.
	cursor   lopper.ID
	offset   int // first visible line of the body
	selected map[lopper.ID]bool
	aliases  []alias // shorten paths, longest directory first
}

func newList(aliases []alias) list {
	return list{selected: map[lopper.ID]bool{}, aliases: aliases}
}

// ids returns worktree IDs in display order.
func ids(s *store) []lopper.ID {
	var out []lopper.ID
	for _, g := range s.groups() {
		for _, r := range g.rows {
			out = append(out, r.worktree.ID)
		}
	}
	return out
}

// current returns the row under the cursor; the first row until the
// user moves.
func (l *list) current(order []lopper.ID) int {
	for i, id := range order {
		if id == l.cursor {
			return i
		}
	}
	return 0
}

func (l *list) update(msg tea.KeyPressMsg, k keyMap, s *store) {
	order := ids(s)
	if len(order) == 0 {
		return
	}
	i := l.current(order)
	switch {
	case key.Matches(msg, k.up):
		i = max(i-1, 0)
	case key.Matches(msg, k.down):
		i = min(i+1, len(order)-1)
	case key.Matches(msg, k.toggle):
		if id := order[i]; l.selected[id] {
			delete(l.selected, id)
		} else {
			l.selected[id] = true
		}
		// Move on, as file managers do: the row leaves the cursor and shows
		// its new state at once, and holding space selects a run of rows.
		i = min(i+1, len(order)-1)
	}
	l.cursor = order[i]
}

// Row layout: rows are indented under their repository and drawn on a
// band one cell wider than the text on each side. The band is faint for
// selected rows, brighter under the cursor and brightest for a selected
// row under the cursor, as with range selection in lazygit; the status
// line shows the cursor row's path.
//
//	 lopper  ~/code                               2 worktrees · 1.5 GB
//	░  feature/login  merged                                1.2 GB ░
//	   fix/typo       not merged                            300 MB
const (
	rowIndent  = 3
	labelWidth = 26
	sizeWidth  = 9
	// minNameWidth is how much of a branch a narrow screen still shows.
	minNameWidth = 12
)

// header renders the title and the scan status.
func header(t theme, s *store, spin string, width int) string {
	title := " " + t.title.Render("lopper")
	var status string
	switch {
	case s.err != nil:
		status = t.failure.Render("scan failed: " + s.err.Error())
	case s.scanning:
		status = t.subtle.Render(fmt.Sprintf("%s scanning · %d found", spin, len(s.order)))
	default:
		status = t.subtle.Render(fmt.Sprintf("%s in %s", plural(len(s.order), "worktree"), plural(len(s.groups()), "repository")))
	}
	return spread(title, status+" ", width)
}

// footer renders the status line and the key help below it. The status
// line shows where the worktree under the cursor is and, at the right
// end under the size column, what is selected; the help drops the keys
// that do not fit.
func (l *list) footer(t theme, s *store, h help.Model, k keyMap, width int) string {
	var picked []*row
	for id := range l.selected {
		if r, ok := s.byID[id]; ok {
			picked = append(picked, r)
		}
	}
	right := ""
	if len(picked) > 0 {
		right = t.selected.Render(fmt.Sprintf("%d selected · %s", len(picked), formatBytes(totalSize(picked)))) + " "
	}
	where := ""
	if order := ids(s); len(order) > 0 {
		wt := s.byID[order[l.current(order)]].worktree
		room := width - 1 - ansi.StringWidth(right)
		if right != "" {
			room -= 2 // keep the path clear of the selection
		}
		where = t.subtle.Render(fitPath(abbrev(wt.Path, l.aliases), max(room, 0)))
	}
	h.SetWidth(max(width-2, 0))
	return spread(" "+where, right, width) + "\n " + h.View(k)
}

// view renders the grouped rows into height lines, scrolling just enough
// to keep the cursor row visible.
func (l *list) view(t theme, s *store, width, height int) string {
	groups := s.groups()
	if len(groups) == 0 {
		msg := "no worktrees found"
		if s.scanning {
			msg = "looking for worktrees…"
		}
		return " " + t.subtle.Render(msg)
	}
	order := ids(s)
	cursor := order[l.current(order)]

	// The name column is as wide as the longest branch, so the facts
	// sit next to their branch whatever the screen width.
	names := 0
	for _, g := range groups {
		for _, r := range g.rows {
			names = max(names, ansi.StringWidth(branchName(r.worktree)))
		}
	}

	var lines []string
	var top, at int // the lines that must stay visible: from top to the cursor row
	for gi, g := range groups {
		if gi > 0 {
			lines = append(lines, "")
		}
		lines = append(lines, l.repoLine(t, g, width))
		for ri, r := range g.rows {
			if r.worktree.ID == cursor {
				top, at = len(lines), len(lines)
				if ri == 0 {
					top-- // bring the repository header along
				}
			}
			lines = append(lines, l.rowLine(t, r, r.worktree.ID == cursor, names, width))
		}
	}

	// View keeps the scroll position so the list only moves when the
	// cursor would leave the screen.
	off := min(l.offset, top)
	off = max(off, at-height+1)
	off = min(off, at) // on a one-line screen, show the cursor row
	off = max(min(off, len(lines)-height), 0)
	l.offset = off
	return strings.Join(lines[off:min(len(lines), off+height)], "\n")
}

func (l *list) repoLine(t theme, g group, width int) string {
	name, where := repoName(g.repo.Path), g.repo.Path
	if filepath.Base(where) == name {
		where = filepath.Dir(where) // the name already says the last part
	}
	where = abbrev(where, l.aliases)
	if g.repo.Path == "" {
		name, where = "unknown repository", ""
	}
	right := plural(len(g.rows), "worktree") + " · " + formatBytes(totalSize(g.rows)) + " "
	room := width - 1 - ansi.StringWidth(name) - 2 - ansi.StringWidth(right) - 2
	left := " " + t.repo.Render(name)
	if room >= 8 {
		left += "  " + t.subtle.Render(fitPath(where, room))
	}
	return spread(left, t.subtle.Render(right), width)
}

func (l *list) rowLine(t theme, r *row, atCursor bool, names, width int) string {
	// Every piece, spaces included, is rendered on the row background: an
	// inner style's reset would otherwise cut the highlight short.
	base, plain, name := lipgloss.NewStyle(), lipgloss.NewStyle(), lipgloss.NewStyle()
	if l.selected[r.worktree.ID] {
		base, name = t.picked, t.selected
	}
	if atCursor {
		base = t.cursor
		if l.selected[r.worktree.ID] {
			base = t.pickedCursor
		}
	}
	seg := func(st lipgloss.Style, s string) string { return st.Inherit(base).Render(s) }

	size := fmt.Sprintf("%*s ", sizeWidth, formatBytes(r.facts.SizeBytes))
	sizeStyle := name
	if r.facts.SizeBytes != nil && *r.facts.SizeBytes < 1_000_000 {
		sizeStyle = sizeStyle.Faint(true) // too small to matter for space
	}
	// The branch and the facts share what the size leaves. A long branch
	// gives way first, down to what keeps it recognizable, then the
	// facts; the size always stays.
	room := width - rowIndent - 2 - ansi.StringWidth(size) - 1
	labelW := max(min(labelWidth, max(room-names, room-minNameWidth)), 0)
	nameW := max(min(names, room-labelW), 0)
	branch := fit(branchName(r.worktree), nameW)
	gap := max(width-rowIndent-nameW-2-labelW-ansi.StringWidth(size), 1)
	var b strings.Builder
	b.WriteString(seg(plain, strings.Repeat(" ", rowIndent)) + seg(name, branch) + seg(plain, "  "))
	// The facts, each in its color, fill labelW cells: the first one that
	// does not fit is cut short, and the rest are left out.
	left := labelW
	for i, n := range facts(r) {
		if i > 0 {
			if left < 4 { // no room for a separator and a letter
				break
			}
			b.WriteString(seg(t.subtle, " · "))
			left -= 3
		}
		text := ansi.Truncate(n.Text, left, "…")
		b.WriteString(seg(t.note[n.Kind], text))
		left -= ansi.StringWidth(text)
	}
	b.WriteString(seg(plain, strings.Repeat(" ", left+gap)) + seg(sizeStyle, size))
	return b.String()
}

// spread puts left and right at the two ends of a width-wide line, or
// just one space apart when they do not fit.
func spread(left, right string, width int) string {
	gap := max(width-ansi.StringWidth(left)-ansi.StringWidth(right), 1)
	return left + strings.Repeat(" ", gap) + right
}

func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	if stem, ok := strings.CutSuffix(noun, "y"); ok {
		return fmt.Sprintf("%d %sies", n, stem)
	}
	return fmt.Sprintf("%d %ss", n, noun)
}
