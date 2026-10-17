package tui

import (
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"charm.land/bubbles/v2/help"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/shinokamix/lopper/apps/lopper/internal/engine"
	"github.com/shinokamix/lopper/apps/lopper/internal/lopper"
)

type removal struct {
	items    []*item
	phase    phase
	next     int
	quitting bool
	frame    int
	offset   int
}

type phase int

const (
	confirming phase = iota
	removing
	finished
)

type item struct {
	row  *row
	done bool
	err  error
}

type removedMsg struct {
	i    int
	size *int64
	err  error
}

const (
	frames    = 48
	frameTime = 25 * time.Millisecond
)

type frameMsg struct{ rm *removal }

func (rm *removal) nextFrame() tea.Cmd {
	return tea.Tick(frameTime, func(time.Time) tea.Msg { return frameMsg{rm} })
}

func (rm *removal) counted(n int64) int64 {
	if rm.frame >= frames {
		return n
	}
	left := 1 - float64(rm.frame)/frames
	return int64(float64(n) * (1 - left*left*left))
}

func newRemoval(rows []*row) *removal {
	rm := &removal{}
	for _, r := range rows {
		rm.items = append(rm.items, &item{row: r})
	}
	rm.sort()
	return rm
}

const (
	safeSection = iota
	workSection
	checkingSection
)

func (it *item) section() int {
	switch {
	case !it.row.checked:
		return checkingSection
	case it.row.verdict.Safe:
		return safeSection
	}
	return workSection
}

func (rm *removal) sort() {
	slices.SortStableFunc(rm.items, func(a, b *item) int { return a.section() - b.section() })
}

func (rm *removal) checked() bool {
	for _, it := range rm.items {
		if !it.row.checked {
			return false
		}
	}
	return true
}

func (rm *removal) confirm() {
	rm.sort()
	for _, it := range rm.items {
		seen := *it.row
		it.row = &seen
	}
	rm.phase = removing
}

func (it *item) force() bool { return !it.row.verdict.Safe }

func (it *item) removed() bool {
	_, left := errors.AsType[*engine.RecordLeftError](it.err)
	return it.done && (it.err == nil || left)
}

func rowsOf(items []*item) []*row {
	out := make([]*row, len(items))
	for i, it := range items {
		out[i] = it.row
	}
	return out
}

func (rm *removal) view(t theme, h help.Model, k keyMap, spin string, width, height int) string {
	if rm.phase == confirming {
		rm.sort()
	}
	verb := "Remove"
	if rm.phase == removing {
		verb = "Removing"
	}
	title := " " + t.title.Render(verb+" "+plural(len(rm.items), "worktree")) +
		t.subtle.Render(" · "+sizeText(rowsOf(rm.items), spin))
	var note string
	switch {
	case rm.quitting:
		note = "stopping after this one…"
	case rm.phase == confirming && !rm.checked():
		note = "checking…"
	}
	head := spread(title, t.subtle.Render(note)+" ", width)

	ls := rm.listing(t, spin, width)
	body := max(height-4, 1)
	shown, scrolls := ls.window(t, &rm.offset, rm.phase == removing, rm.next, body)

	var keys bindings
	switch {
	case rm.phase == confirming:
		if scrolls {
			keys = append(keys, k.scroll)
		}
		keys = append(keys, k.confirm, k.back)
	case rm.phase == removing && !rm.quitting:
		keys = append(keys, k.stop)
	}
	h.SetWidth(max(width-2, 0))
	return head + "\n\n" + strings.Join(shown, "\n") + "\n\n " + h.View(keys)
}

type listing struct {
	indent int
	text   []string
	item   []int
	head   []int
}

func (rm *removal) listing(t theme, spin string, width int) listing {
	names, notes := columns(rowsOf(rm.items))
	repoW := 0
	for _, it := range rm.items {
		repoW = max(repoW, ansi.StringWidth(repoLabel(it.row.worktree.Repo)))
	}
	repoW = min(repoW, width/5)
	headings := map[int]string{
		workSection:     t.failure.Render("removal may lose work"),
		checkingSection: t.subtle.Render("still checking"),
	}
	plain := lipgloss.NewStyle()
	ls := listing{indent: rowIndent}
	add := func(text string, item, head int) {
		ls.text, ls.item, ls.head = append(ls.text, text), append(ls.item, item), append(ls.head, head)
	}
	head := -1
	for i, it := range rm.items {
		if sec := it.section(); i == 0 || sec != rm.items[i-1].section() {
			if i > 0 {
				add("", -1, head)
			}
			head = -1
			if heading, ok := headings[sec]; ok {
				head = len(ls.text)
				add(" "+heading, -1, head)
			}
		}
		mark := " "
		switch {
		case it.done && it.removed():
			mark = "✓"
		case it.done:
			mark = t.failure.Render("✗")
		case rm.phase == removing && i == rm.next:
			mark = spin
		}
		lead := " " + mark + " " + t.subtle.Render(fit(repoLabel(it.row.worktree.Repo), repoW)) + "  "
		add(rowLine(t, it.row, lead, plain, plain, spin, names, notes, width), i, head)
	}
	return ls
}

func (ls listing) window(t theme, offset *int, following bool, focus, height int) ([]string, bool) {
	all := len(ls.text)
	if all <= height {
		*offset = 0
		return ls.text, false
	}
	layout := func(off int) (from, to int, pin, below bool) {
		n := height
		if off > 0 {
			n--
		}
		if pin = off > 0 && ls.head[off] >= 0 && ls.head[off] < off; pin {
			n--
		}
		if off+n < all {
			n--
			below = true
		}
		return off, off + max(n, 1), pin, below
	}
	off := max(min(*offset, all-1), 0)
	if following {
		at := slices.Index(ls.item, focus)
		for off > 0 && off > at {
			off--
		}
		for _, to, _, _ := layout(off); at >= to && off < all-1; _, to, _, _ = layout(off) {
			off++
		}
	}
	for off > 0 {
		if _, _, _, below := layout(off - 1); below {
			break
		}
		off--
	}
	*offset = off

	from, to, pin, below := layout(off)
	count := func(lo, hi int) int {
		n := 0
		for _, it := range ls.item[lo:hi] {
			if it >= 0 {
				n++
			}
		}
		return n
	}
	more := func(arrow string, n int) string {
		return strings.Repeat(" ", ls.indent) + t.subtle.Render(fmt.Sprintf("%s %d more", arrow, n))
	}
	var out []string
	if off > 0 {
		out = append(out, more("↑", count(0, off)))
	}
	if pin {
		out = append(out, ls.text[ls.head[off]])
	}
	out = append(out, ls.text[from:min(to, all)]...)
	if below {
		out = append(out, more("↓", count(to, all)))
	}
	return out, true
}

func (rm *removal) summary(t theme, h help.Model, k keyMap, width, height int) string {
	var removed, failed, left []*item
	for _, it := range rm.items {
		switch {
		case !it.done:
		case it.removed():
			removed = append(removed, it)
			if it.err != nil {
				left = append(left, it)
			}
		default:
			failed = append(failed, it)
		}
	}

	count := plural(len(removed), "worktree") + " removed"
	var lines []string
	switch size := totalSize(rowsOf(removed)); {
	case len(removed) == 0:
		lines = append(lines, t.title.Render("nothing removed"))
	case size != nil:
		total := formatBytes(*size)
		now := fmt.Sprintf("%*s", len(total), formatBytes(rm.counted(*size)))
		lines = append(lines, t.title.Render(now+" freed"), t.subtle.Render(count))
	default:
		lines = append(lines, t.title.Render(count))
	}

	branchW := 0
	for _, it := range append(failed, left...) {
		branchW = max(branchW, ansi.StringWidth(branchName(it.row.worktree)))
	}
	branchW = min(branchW, width/3)
	var tbl listing
	var titles []string
	section := func(title string, items []*item) {
		if len(items) == 0 {
			return
		}
		if len(tbl.text) > 0 {
			tbl.text, tbl.item, tbl.head = append(tbl.text, ""), append(tbl.item, -1), append(tbl.head, -1)
		}
		head := len(tbl.text)
		titles = append(titles, title)
		tbl.text, tbl.item, tbl.head = append(tbl.text, title), append(tbl.item, -1), append(tbl.head, head)
		for i, it := range items {
			row := fit(branchName(it.row.worktree), branchW) + "  " + t.subtle.Render(reason(it.err))
			tbl.text, tbl.item, tbl.head = append(tbl.text, row), append(tbl.item, i), append(tbl.head, head)
		}
	}
	section(t.failure.Render(fmt.Sprintf("%d not removed", len(failed))), failed)
	section(t.subtle.Render("git still lists "+plural(len(left), "removed worktree")), left)

	var table []string
	scrolls := false
	switch room := height - len(lines) - 3; {
	case len(tbl.text) == 0:
	case room < 3:
		table = titles[:min(len(titles), max(room, 1))]
	default:
		table, scrolls = tbl.window(t, &rm.offset, false, 0, room)
	}
	tableW := 0
	for i, l := range table {
		table[i] = ansi.Truncate(l, width, "…")
		tableW = max(tableW, ansi.StringWidth(table[i]))
	}
	if len(table) > 0 {
		lines = append(lines, "")
	}
	for _, l := range table {
		lines = append(lines, l+strings.Repeat(" ", tableW-ansi.StringWidth(l)))
	}

	keys := bindings{k.rescan, k.back, k.quit}
	if scrolls {
		keys = append(bindings{k.scroll}, keys...)
	}
	h.SetWidth(max(width-2, 0))
	for range min(max(height-len(lines)-1, 0), 2) {
		lines = append(lines, "")
	}
	lines = append(lines, h.View(keys))
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, strings.Join(lines, "\n"))
}

func repoLabel(repo lopper.Repo) string {
	if repo.Path == "" {
		return "unknown"
	}
	return repoName(repo.Path)
}

func reason(err error) string {
	if e, ok := errors.AsType[*engine.NotSafeError](err); ok {
		return "not safe anymore: " + strings.Join(e.Verdict.Reasons(), noteSep)
	}
	if e, ok := errors.AsType[*engine.RecordLeftError](err); ok {
		return "run git worktree prune in " + repoLabel(lopper.Repo{Path: e.Repo})
	}
	return quoted.ReplaceAllStringFunc(err.Error(), func(q string) string {
		if p := strings.Trim(q, "'"); filepath.IsAbs(p) {
			return "'" + filepath.Base(p) + "'"
		}
		return q
	})
}

// quoted matches the single quotes git puts around paths in its messages.
var quoted = regexp.MustCompile(`'[^']+'`)
