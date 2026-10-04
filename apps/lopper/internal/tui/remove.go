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
	"github.com/shinokamix/lopper/apps/lopper/internal/verdict"
)

// removal is the screen that removes worktrees. It shows what goes and asks
// once, removes them one at a time, then reports the outcome.
type removal struct {
	// items are the safe ones first, then the rest. They follow the scan
	// until the user confirms, then stay as confirmed.
	items    []*item
	phase    phase
	next     int  // the item being removed
	quitting bool // quit once the item being removed is done
	frame    int  // of the freed space counting up after the removal
	offset   int  // first visible line of the worktrees
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
	err  error // why it was not removed, or a *engine.RecordLeftError
}

// removedMsg reports how removing item i went, and its size.
type removedMsg struct {
	i    int
	size *int64
	err  error
}

// The space freed counts up from zero in frames, slowing down as it
// nears the total.
const (
	frames    = 48
	frameTime = 25 * time.Millisecond
)

// frameMsg asks rm to show its next frame.
type frameMsg struct{ rm *removal }

func (rm *removal) nextFrame() tea.Cmd {
	return tea.Tick(frameTime, func(time.Time) tea.Msg { return frameMsg{rm} })
}

// counted is how much of n the counter shows, all of it once done.
func (rm *removal) counted(n int64) int64 {
	if rm.frame >= frames {
		return n
	}
	left := 1 - float64(rm.frame)/frames
	return int64(float64(n) * (1 - left*left*left)) // ease out
}

func newRemoval(rows []*row) *removal {
	rm := &removal{}
	for _, r := range rows {
		rm.items = append(rm.items, &item{row: r})
	}
	rm.sort()
	return rm
}

// The sections of the removal screen, in display order.
const (
	safeSection     = iota
	workSection     // not safe to delete
	checkingSection // facts still to come
)

func (it *item) section() int {
	switch {
	case !it.row.checked:
		return checkingSection
	case it.row.safe:
		return safeSection
	}
	return workSection
}

// sort orders the items by section, since a verdict may change while the
// user looks.
func (rm *removal) sort() {
	slices.SortStableFunc(rm.items, func(a, b *item) int { return a.section() - b.section() })
}

// checked reports whether the facts of every item have arrived. Until then
// the user cannot see what would be lost and cannot confirm. An item still
// being checked shows as not safe.
func (rm *removal) checked() bool {
	for _, it := range rm.items {
		if !it.row.checked {
			return false
		}
	}
	return true
}

// confirm freezes the items as the user sees them, since the scan may still
// update them.
func (rm *removal) confirm() {
	rm.sort()
	for _, it := range rm.items {
		seen := *it.row
		it.row = &seen
	}
	rm.phase = removing
}

// force reports whether to remove the item whatever it holds, because the
// user saw that it is not safe and confirmed. An item shown as safe is not
// forced, so work that appears in it after the scan stops its removal.
func (it *item) force() bool { return !it.row.safe }

// removed reports whether the worktree is gone from disk.
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

// view renders the confirmation or the progress in height lines, with the
// worktrees and a line of key help.
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
		note = "checking…" // enter waits for it
	}
	head := spread(title, t.subtle.Render(note)+" ", width)

	ls := rm.listing(t, spin, width)
	body := max(height-4, 1) // the title, the key help and a blank line after each
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

// listing holds the lines of the screen, some of them items in sections.
type listing struct {
	indent int // of the "N more" lines
	text   []string
	item   []int // the item on each line, or -1
	head   []int // the heading over each line's section, or -1
}

func (rm *removal) listing(t theme, spin string, width int) listing {
	names, notes := columns(rowsOf(rm.items))
	// Unlike the list, this screen does not group rows by repository, and
	// branch names repeat across repositories, so each row starts with its
	// repository.
	repoW := 0
	for _, it := range rm.items {
		repoW = max(repoW, ansi.StringWidth(repoLabel(it.row.worktree.Repo)))
	}
	repoW = min(repoW, width/5) // the branch says more, so a long repository name shrinks
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
				// Align the heading with the title, as the list does repository
				// headers, so it reads as a heading over the rows.
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

// window returns the lines that fit in height from *offset, which it keeps
// in range, and whether some lines did not fit. When following, it scrolls
// just enough to show item focus. It counts the lines out of sight in their
// place and keeps a section's heading on top while its rows are in sight.
func (ls listing) window(t theme, offset *int, following bool, focus, height int) ([]string, bool) {
	all := len(ls.text)
	if all <= height {
		*offset = 0
		return ls.text, false
	}
	// layout returns, for first line off, the lines shown below the marks and
	// the pinned heading, and whether any lines remain below.
	layout := func(off int) (from, to int, pin, below bool) {
		n := height
		if off > 0 {
			n-- // "↑ N more"
		}
		if pin = off > 0 && ls.head[off] >= 0 && ls.head[off] < off; pin {
			n--
		}
		if off+n < all {
			n-- // "↓ N more"
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
	for off > 0 { // back up while the last line still shows, to never pass the end
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

// summary reports the outcome of the removal, centered in width × height,
// with the keys that lead on.
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
		// Pad to the width of the total, so the line stays put as the counter
		// passes through the units.
		total := formatBytes(*size)
		now := fmt.Sprintf("%*s", len(total), formatBytes(rm.counted(*size)))
		lines = append(lines, t.title.Render(now+" freed"), t.subtle.Render(count))
	default: // some sizes were still being measured
		lines = append(lines, t.title.Render(count))
	}

	// What was not removed and what git still lists go in a table that
	// scrolls when it does not fit. Its lines share one width, no wider than
	// the screen, so centering keeps them aligned.
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
	switch room := height - len(lines) - 3; { // what the key help and two blank lines leave
	case len(tbl.text) == 0:
	case room < 3:
		// Too short to scroll. Still say how many, or the summary reads as if
		// everything went.
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
		lines = append(lines, "") // two blank lines above the key help when they fit
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

// reason says why a worktree was not removed, in the list's words, or
// what is left to do when git still lists it.
func reason(err error) string {
	if e, ok := errors.AsType[*engine.NotSafeError](err); ok {
		var why []string
		for _, n := range verdict.Notes(e.Worktree, e.Facts) {
			why = append(why, n.Text)
		}
		return "not safe anymore: " + strings.Join(why, noteSep)
	}
	if e, ok := errors.AsType[*engine.RecordLeftError](err); ok {
		return "run git worktree prune in " + repoLabel(lopper.Repo{Path: e.Repo})
	}
	// The row already names the worktree, and a path in the message only
	// pushes the cause out of sight.
	return quoted.ReplaceAllStringFunc(err.Error(), func(q string) string {
		if p := strings.Trim(q, "'"); filepath.IsAbs(p) {
			return "'" + filepath.Base(p) + "'"
		}
		return q
	})
}

// quoted matches what git quotes in its messages, paths among them.
var quoted = regexp.MustCompile(`'[^']+'`)
