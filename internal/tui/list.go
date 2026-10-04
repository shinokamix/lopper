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
// owns its UI state: the cursor, the scroll position, the selection, the
// order and the search.
type list struct {
	// cursor follows a worktree, not a position: a worktree found later
	// may land above the cursor.
	cursor lopper.ID
	offset int // first visible line of the body
	// line is the screen line of the cursor row: the row stays on it while
	// the list changes around it, until the user moves.
	line     int
	moved    bool
	selected map[lopper.ID]bool
	aliases  []alias // shorten paths, longest directory first
	// bySize shows the largest worktrees first, as ranked by ranks: a
	// scan re-ranks them now and then rather than on every size.
	bySize bool
	ranks  ranks
	// query hides the rows that do not match it; searching is set while
	// it is being typed.
	query     string
	searching bool
	// allKeys shows every key in place of the rows, scrolled by keysOffset.
	allKeys    bool
	keysOffset int
}

func newList(aliases []alias) list {
	return list{selected: map[lopper.ID]bool{}, aliases: aliases, moved: true}
}

// groups returns the groups shown: the rows matching the query, in the
// order found or largest first.
func (l *list) groups(s *store) []group {
	terms := strings.Fields(strings.ToLower(l.query))
	var out []group
	for _, g := range s.groups() {
		var rows []*row
		for _, r := range g.rows {
			if matches(r, terms) {
				rows = append(rows, r)
			}
		}
		if len(rows) > 0 {
			out = append(out, group{repo: g.repo, rows: rows, found: g.found})
		}
	}
	if l.bySize {
		l.ranks.sort(out)
	}
	return out
}

// ids returns the IDs of the rows shown, in display order.
func (l *list) ids(s *store) []lopper.ID {
	var out []lopper.ID
	for _, g := range l.groups(s) {
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
	order := l.ids(s)
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
	l.cursor, l.moved = order[i], true
}

// sort switches between the order found and largest first. The cursor
// row keeps its place on screen while the others move around it.
func (l *list) sort(s *store) {
	l.pin(s)
	l.bySize = !l.bySize
	l.rank(s)
}

// rank ranks the rows by their size now, when shown largest first.
func (l *list) rank(s *store) {
	if l.bySize {
		l.pin(s)
		l.ranks = sizeRanks(s.groups())
	}
}

// pin puts the cursor on the worktree it shows, before the rows move: it
// shows the first row while the user has not moved, and that row may
// not stay first.
func (l *list) pin(s *store) {
	if order := l.ids(s); len(order) > 0 {
		l.cursor = order[l.current(order)]
	}
}

// search changes the query to q.
func (l *list) search(q string) {
	l.query, l.moved = q, true
}

// searchKey handles a key typed into the search: enter keeps the query,
// esc clears it, the arrows move the cursor and text goes into it.
func (l *list) searchKey(msg tea.KeyPressMsg, k keyMap, s *store) {
	switch {
	case key.Matches(msg, k.done):
		l.searching = false
	case key.Matches(msg, k.clear):
		l.searching = false
		l.search("")
	case key.Matches(msg, k.move):
		l.update(msg, k, s)
	case msg.Code == tea.KeyBackspace:
		q := []rune(l.query)
		l.search(string(q[:max(len(q)-1, 0)]))
	case msg.Text != "":
		l.search(l.query + msg.Text)
	}
}

// matches reports whether r matches every term: a term matches a row
// whose branch or path contains it, or with a fact that starts with it
// once counts are left out, so "merged" matches "merged" and not "not
// merged", and "unpushed" matches "3 unpushed". "safe" matches the rows
// safe to delete.
func matches(r *row, terms []string) bool {
	for _, term := range terms {
		if !matchesTerm(r, term) {
			return false
		}
	}
	return true
}

func matchesTerm(r *row, term string) bool {
	if term == "safe" && r.safe {
		return true
	}
	for _, s := range []string{branchName(r.worktree), r.worktree.Path, r.worktree.Repo.Path} {
		if strings.Contains(strings.ToLower(s), term) {
			return true
		}
	}
	for _, n := range facts(r) {
		if strings.HasPrefix(strings.TrimLeft(strings.ToLower(n.Text), "0123456789 "), term) {
			return true
		}
	}
	return false
}

// targets returns what the remove key acts on: the selected rows, shown
// or hidden by the search, or the row under the cursor when none is
// selected.
func (l *list) targets(s *store) (rows []*row) {
	order := l.ids(s)
	if len(l.selected) == 0 {
		if len(order) == 0 {
			return nil
		}
		return []*row{s.byID[order[l.current(order)]]}
	}
	shown := map[lopper.ID]bool{}
	for _, id := range order {
		shown[id] = true
		if l.selected[id] {
			rows = append(rows, s.byID[id])
		}
	}
	for _, id := range s.order {
		if l.selected[id] && !shown[id] {
			rows = append(rows, s.byID[id])
		}
	}
	return rows
}

// drop takes a removed worktree off the list. The cursor moves to the
// row below it, or above when it was the last, instead of to the top. The
// totals change, so the list shown largest first is ranked again.
func (l *list) drop(s *store, id lopper.ID) {
	l.pin(s) // a hidden row the cursor was on is not the one it shows
	if order := l.ids(s); id == l.cursor {
		i := l.current(order)
		switch {
		case i+1 < len(order):
			l.cursor = order[i+1]
		case i > 0:
			l.cursor = order[i-1]
		}
	}
	delete(l.selected, id)
	s.remove(id)
	l.rank(s)
}

// Row layout: rows are indented under their repository and drawn on a
// band one cell wider than the text on each side. The band is faint for
// selected rows, brighter under the cursor and brightest for a selected
// row under the cursor, as with range selection in lazygit; the status
// line shows the cursor row's path. The branch column is as wide
// whatever the branches, so the facts never move as worktrees are found.
//
//	 lopper  ~/code                               2 worktrees · 1.5 GB
//	░  feature/login  merged                                1.2 GB ░
//	   fix/typo       3 uncommitted · not merged            300 MB
const (
	rowIndent = 3
	sizeWidth = 9
	// minNameWidth is how much of a branch a narrow screen still shows,
	// maxNameWidth how much a wide one gives it.
	minNameWidth = 12
	maxNameWidth = 40
)

// nameWidth is the width of the branch column on a width-wide screen: a
// third of what the size leaves, so the facts have the rest.
func nameWidth(width int) int {
	room := width - rowIndent - 2 - (sizeWidth + 1) - 1
	return min(max(room/3, minNameWidth), maxNameWidth)
}

// header renders the title, the search and, at the right end, the order
// and the scan status.
func (l *list) header(t theme, s *store, spin string, width int) string {
	left := " " + t.title.Render("lopper")
	switch {
	case l.searching && l.query == "":
		left += "  / " + t.caret.Render(" ") + t.subtle.Render("branch, path, safe, merged…")
	case l.searching:
		left += "  / " + l.query + t.caret.Render(" ")
	case l.query != "":
		left += "  / " + l.query
	}
	shown, found := len(l.ids(s)), len(s.order)
	var status string
	switch {
	case s.err != nil:
		status = t.failure.Render("scan failed: " + s.err.Error())
	case s.scanning && !s.listed:
		status = t.subtle.Render(fmt.Sprintf("%s checking known repositories · %d found", spin, found))
	case s.scanning && l.query != "":
		status = t.subtle.Render(fmt.Sprintf("%s scanning · %d of %d found", spin, shown, found))
	case s.scanning:
		status = t.subtle.Render(fmt.Sprintf("%s scanning · %d found", spin, found))
	case l.query != "":
		status = t.subtle.Render(fmt.Sprintf("%d of %s", shown, plural(found, "worktree")))
	default:
		status = t.subtle.Render(fmt.Sprintf("%s in %s", plural(found, "worktree"), plural(len(s.groups()), "repository")))
	}
	if l.bySize {
		status = t.subtle.Render("largest first · ") + status
	}
	return spread(left, status+" ", width)
}

// footer renders the status line and the key help below it. The status
// line shows where the worktree under the cursor is and, at the right
// end under the size column, what is selected; the help drops the keys
// that do not fit.
func (l *list) footer(t theme, s *store, h help.Model, k keyMap, spin string, width int) string {
	order := l.ids(s)
	shown := map[lopper.ID]bool{}
	for _, id := range order {
		shown[id] = true
	}
	var picked []*row
	hidden := 0
	for id := range l.selected {
		if r, ok := s.byID[id]; ok {
			picked = append(picked, r)
			if !shown[id] {
				hidden++
			}
		}
	}
	right := ""
	if len(picked) > 0 {
		n := fmt.Sprintf("%d selected", len(picked))
		if hidden > 0 {
			n += fmt.Sprintf(" (%d hidden)", hidden)
		}
		right = t.selected.Render(n+" · "+sizeText(picked, spin)) + " "
	}
	where := ""
	if len(order) > 0 {
		wt := s.byID[order[l.current(order)]].worktree
		room := width - 1 - ansi.StringWidth(right)
		if right != "" {
			room -= 2 // keep the path clear of the selection
		}
		where = t.subtle.Render(fitPath(abbrev(wt.Path, l.aliases), max(room, 0)))
	}
	h.SetWidth(max(width-2, 0))
	return spread(" "+where, right, width) + "\n " + l.keyHelp(h, k)
}

// keyHelp renders the keys of the list: while searching, those of the
// search; with every key shown, how to scroll and close them; otherwise
// the main ones.
func (l *list) keyHelp(h help.Model, k keyMap) string {
	if l.searching {
		return h.ShortHelpView(bindings{k.done, k.clear, k.move})
	}
	if l.allKeys {
		hide := k.help
		hide.SetHelp("?", "close")
		return fitKeys(h, bindings{k.scroll, k.quit}, hide)
	}
	return fitKeys(h, bindings{k.toggle, k.remove, l.find(k), k.sort, k.quit}, k.help)
}

// find is the key that searches, or clears the search once there is one.
func (l *list) find(k keyMap) key.Binding {
	if l.query != "" {
		return k.clear
	}
	return k.search
}

// fitKeys renders keys and then last in h.Width() cells. last stays: where
// the others do not all fit, it is how to find them, so they give way
// instead, the last first.
func fitKeys(h help.Model, keys bindings, last key.Binding) string {
	width := h.Width()
	h.SetWidth(0) // measured whole, not cut off
	for ; len(keys) > 0; keys = keys[:len(keys)-1] {
		if line := h.ShortHelpView(append(keys, last)); ansi.StringWidth(line) <= width {
			return line
		}
	}
	return h.ShortHelpView(bindings{last})
}

// keysView renders every key of the list into height lines, in place of
// the rows: in groups side by side, or one under another where they do
// not fit, as cut off a group would hide its keys; scrolled where they
// are taller than the screen.
func (l *list) keysView(h help.Model, k keyMap, width, height int) string {
	groups := [][]key.Binding{{k.up, k.down, k.toggle}, {k.remove, l.find(k), k.sort, k.quit}}
	h.SetWidth(0)
	view := h.FullHelpView(groups)
	if lipgloss.Width(view) > width-1 { // its widest line
		var stacked []string
		for _, g := range groups {
			stacked = append(stacked, h.FullHelpView([][]key.Binding{g}))
		}
		view = strings.Join(stacked, "\n")
	}
	lines := strings.Split(view, "\n")
	l.keysOffset = max(min(l.keysOffset, len(lines)-height), 0)
	lines = lines[l.keysOffset:min(len(lines), l.keysOffset+height)]
	return " " + strings.Join(lines, "\n ")
}

// view renders the grouped rows into height lines. It scrolls just
// enough to keep the cursor row visible when the user moves, and keeps
// the cursor row on its screen line when the list changes around it.
// Sizes still being measured show spin, the spinner's frame.
func (l *list) view(t theme, s *store, spin string, width, height int) string {
	groups := l.groups(s)
	if len(groups) == 0 {
		var msg string
		switch {
		case l.query != "" && s.scanning:
			msg = "no matches yet"
		case l.query != "":
			msg = "no matches"
		case s.scanning:
			msg = "looking for worktrees…"
		default:
			msg = "no worktrees found"
		}
		return " " + t.subtle.Render(msg)
	}
	order := l.ids(s)
	cursor := order[l.current(order)]

	names := nameWidth(width)
	notes := max(width-rowIndent-2-(sizeWidth+1)-1-names, 0)
	var lines []string
	var top, at int // the lines that must stay visible: from top to the cursor row
	for gi, g := range groups {
		if gi > 0 {
			lines = append(lines, "")
		}
		lines = append(lines, l.repoLine(t, g, spin, width))
		for ri, r := range g.rows {
			if r.worktree.ID == cursor {
				top, at = len(lines), len(lines)
				if ri == 0 {
					top-- // bring the repository header along
				}
			}
			lines = append(lines, l.rowLine(t, r, r.worktree.ID == cursor, spin, names, notes, width))
		}
	}

	// View keeps the scroll position so the list only moves when the
	// cursor would leave the screen, or the cursor row on its line when
	// the user did not move it.
	off := l.offset
	if !l.moved {
		off = at - l.line
	}
	off = min(off, top)
	off = max(off, at-height+1)
	off = min(off, at) // on a one-line screen, show the cursor row
	off = max(min(off, len(lines)-height), 0)
	l.offset, l.line, l.moved = off, at-off, false
	return strings.Join(lines[off:min(len(lines), off+height)], "\n")
}

func (l *list) repoLine(t theme, g group, spin string, width int) string {
	name, where := repoName(g.repo.Path), g.repo.Path
	if filepath.Base(where) == name {
		where = filepath.Dir(where) // the name already says the last part
	}
	where = abbrev(where, l.aliases)
	if g.repo.Path == "" {
		name, where = "unknown repository", ""
	}
	count := plural(g.found, "worktree")
	if len(g.rows) < g.found {
		count = fmt.Sprintf("%d of %s", len(g.rows), count)
	}
	right := count + " · " + sizeText(g.rows, spin) + " "
	room := width - 1 - ansi.StringWidth(name) - 2 - ansi.StringWidth(right) - 2
	left := " " + t.repo.Render(name)
	if room >= 8 {
		left += "  " + t.subtle.Render(fitPath(where, room))
	}
	return spread(left, t.subtle.Render(right), width)
}

// columns returns the widths of the name and facts columns for rows. The
// name column is as wide as the longest branch, so the facts sit next to
// their branch whatever the screen width, and the facts column as wide
// as the longest facts, so they all show when they fit.
func columns(rows []*row) (names, notes int) {
	for _, r := range rows {
		names = max(names, ansi.StringWidth(branchName(r.worktree)))
		notes = max(notes, notesWidth(facts(r)))
	}
	return names, notes
}

// rowLine renders a row of the list. A row still being measured is
// faint until its size arrives.
func (l *list) rowLine(t theme, r *row, atCursor bool, spin string, names, notes, width int) string {
	base, name := lipgloss.NewStyle(), lipgloss.NewStyle()
	if l.selected[r.worktree.ID] {
		base, name = t.picked, t.selected
	} else if r.facts.SizeBytes == nil && !r.final {
		base = base.Faint(true)
	}
	if atCursor {
		base = base.Inherit(t.cursor)
		if l.selected[r.worktree.ID] {
			base = t.pickedCursor
		}
	}
	return rowLine(t, r, base.Render(strings.Repeat(" ", rowIndent)), base, name, spin, names, notes, width)
}

// rowLine renders a worktree: lead, at least rowIndent cells wide, then
// its branch and facts in columns names and notes wide, and its size at
// the right end, or spin while it is being measured, on the background
// of base.
func rowLine(t theme, r *row, lead string, base, name lipgloss.Style, spin string, names, notes, width int) string {
	// Every piece, spaces included, is rendered on the row background: an
	// inner style's reset would otherwise cut the highlight short.
	plain := lipgloss.NewStyle()
	seg := func(st lipgloss.Style, s string) string { return st.Inherit(base).Render(s) }

	size := fmt.Sprintf("%*s ", sizeWidth, sizeText([]*row{r}, spin))
	sizeStyle := name
	if r.facts.SizeBytes == nil || *r.facts.SizeBytes < 1_000_000 {
		sizeStyle = sizeStyle.Faint(true) // too small to matter for space, or not known
	}
	// The branch and the facts share what the size leaves. A long branch
	// gives way first, down to what keeps it recognizable, then the
	// facts; the size always stays.
	indent := ansi.StringWidth(lead)
	room := width - indent - 2 - ansi.StringWidth(size) - 1
	labelW := max(min(notes, max(room-names, room-minNameWidth)), 0)
	nameW := max(min(names, room-labelW), 0)
	branch := fit(branchName(r.worktree), nameW)
	gap := max(width-indent-nameW-2-labelW-ansi.StringWidth(size), 1)
	var b strings.Builder
	b.WriteString(lead + seg(name, branch) + seg(plain, "  "))
	// The facts, each in its color, fill labelW cells. Those that do not
	// fit are counted, most pressing first so the least are left out.
	shown, hidden := fitNotes(facts(r), labelW)
	var parts []string
	for _, n := range shown {
		parts = append(parts, seg(t.note[n.Kind], n.Text))
	}
	label := strings.Join(parts, seg(t.subtle, noteSep))
	if hidden > 0 {
		count := more(hidden)
		if len(shown) == 0 {
			count = strings.TrimSpace(count)
		}
		label += seg(t.subtle, count)
	}
	label = ansi.Truncate(label, labelW, "…") // not even the count fits
	b.WriteString(label)
	left := labelW - ansi.StringWidth(label)
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
