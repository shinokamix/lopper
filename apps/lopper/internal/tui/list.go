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

	"github.com/shinokamix/lopper/apps/lopper/internal/lopper"
)

type list struct {
	cursor     lopper.ID
	offset     int
	line       int
	moved      bool
	selected   map[lopper.ID]bool
	aliases    []alias
	bySize     bool
	ranks      ranks
	query      string
	searching  bool
	allKeys    bool
	keysOffset int
}

func newList(aliases []alias) list {
	return list{selected: map[lopper.ID]bool{}, aliases: aliases, moved: true}
}

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

func (l *list) ids(s *store) []lopper.ID {
	var out []lopper.ID
	for _, g := range l.groups(s) {
		for _, r := range g.rows {
			out = append(out, r.worktree.ID)
		}
	}
	return out
}

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
		i = min(i+1, len(order)-1)
	}
	l.cursor, l.moved = order[i], true
}

func (l *list) sort(s *store) {
	l.pin(s)
	l.bySize = !l.bySize
	l.rank(s)
}

func (l *list) rank(s *store) {
	if l.bySize {
		l.pin(s)
		l.ranks = sizeRanks(s.groups())
	}
}

func (l *list) pin(s *store) {
	if order := l.ids(s); len(order) > 0 {
		l.cursor = order[l.current(order)]
	}
}

func (l *list) search(q string) {
	l.query, l.moved = q, true
}

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

func matches(r *row, terms []string) bool {
	for _, term := range terms {
		if !matchesTerm(r, term) {
			return false
		}
	}
	return true
}

func matchesTerm(r *row, term string) bool {
	if term == "safe" && r.verdict.Safe {
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

func (l *list) drop(s *store, id lopper.ID) {
	l.pin(s)
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

const (
	rowIndent    = 3
	sizeWidth    = 9
	minNameWidth = 12
	maxNameWidth = 40
)

func nameWidth(width int) int {
	room := width - rowIndent - 2 - (sizeWidth + 1) - 1
	return min(max(room/3, minNameWidth), maxNameWidth)
}

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
			room -= 2
		}
		where = t.subtle.Render(fitPath(abbrev(wt.Path, l.aliases), max(room, 0)))
	}
	h.SetWidth(max(width-2, 0))
	return spread(" "+where, right, width) + "\n " + l.keyHelp(h, k)
}

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

func (l *list) find(k keyMap) key.Binding {
	if l.query != "" {
		return k.clear
	}
	return k.search
}

func fitKeys(h help.Model, keys bindings, last key.Binding) string {
	width := h.Width()
	h = untruncated(h)
	for ; len(keys) > 0; keys = keys[:len(keys)-1] {
		if line := h.ShortHelpView(append(keys, last)); ansi.StringWidth(line) <= width {
			return line
		}
	}
	return h.ShortHelpView(bindings{last})
}

func untruncated(h help.Model) help.Model {
	h.SetWidth(0)
	return h
}

func (l *list) keysView(h help.Model, k keyMap, width, height int) string {
	groups := [][]key.Binding{{k.up, k.down, k.toggle}, {k.remove, l.find(k), k.sort, k.quit}}
	h = untruncated(h)
	view := h.FullHelpView(groups)
	if lipgloss.Width(view) > width-1 {
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
	var top, at int
	for gi, g := range groups {
		if gi > 0 {
			lines = append(lines, "")
		}
		lines = append(lines, l.repoLine(t, g, spin, width))
		for ri, r := range g.rows {
			if r.worktree.ID == cursor {
				top, at = len(lines), len(lines)
				if ri == 0 {
					top--
				}
			}
			lines = append(lines, l.rowLine(t, r, r.worktree.ID == cursor, spin, names, notes, width))
		}
	}

	off := l.offset
	if !l.moved {
		off = at - l.line
	}
	off = min(off, top)
	off = max(off, at-height+1)
	off = min(off, at)
	off = max(min(off, len(lines)-height), 0)
	l.offset, l.line, l.moved = off, at-off, false
	return strings.Join(lines[off:min(len(lines), off+height)], "\n")
}

func (l *list) repoLine(t theme, g group, spin string, width int) string {
	name, where := repoName(g.repo.Path), g.repo.Path
	if filepath.Base(where) == name {
		where = filepath.Dir(where)
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

func columns(rows []*row) (names, notes int) {
	for _, r := range rows {
		names = max(names, ansi.StringWidth(branchName(r.worktree)))
		notes = max(notes, notesWidth(facts(r)))
	}
	return names, notes
}

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

func rowLine(t theme, r *row, lead string, base, name lipgloss.Style, spin string, names, notes, width int) string {
	// A styled piece ends in a reset that clears the background, so every piece
	// renders on base.
	plain := lipgloss.NewStyle()
	seg := func(st lipgloss.Style, s string) string { return st.Inherit(base).Render(s) }

	size := fmt.Sprintf("%*s ", sizeWidth, sizeText([]*row{r}, spin))
	sizeStyle := name
	if r.facts.SizeBytes == nil || *r.facts.SizeBytes < 1_000_000 {
		sizeStyle = sizeStyle.Faint(true)
	}
	indent := ansi.StringWidth(lead)
	room := width - indent - 2 - ansi.StringWidth(size) - 1
	labelW := max(min(notes, max(room-names, room-minNameWidth)), 0)
	nameW := max(min(names, room-labelW), 0)
	branch := fit(branchName(r.worktree), nameW)
	gap := max(width-indent-nameW-2-labelW-ansi.StringWidth(size), 1)
	var b strings.Builder
	b.WriteString(lead + seg(name, branch) + seg(plain, "  "))
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
	label = ansi.Truncate(label, labelW, "…")
	b.WriteString(label)
	left := labelW - ansi.StringWidth(label)
	b.WriteString(seg(plain, strings.Repeat(" ", left+gap)) + seg(sizeStyle, size))
	return b.String()
}

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
