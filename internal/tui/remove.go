package tui

import (
	"errors"
	"fmt"
	"strings"

	"charm.land/bubbles/v2/help"
	"charm.land/lipgloss/v2"

	"github.com/shinokamix/lopper/internal/engine"
	"github.com/shinokamix/lopper/internal/lopper"
)

// removal is the screen that removes worktrees: it shows what goes and
// asks once, removes them one at a time, then tells what came of it.
type removal struct {
	// items are safe ones first, then the ones that are not, each as the
	// user saw it when asked.
	items     []*item
	unchecked int // left out: their facts had not arrived yet
	phase     phase
	next      int  // the item being removed
	quitting  bool // quit once the item being removed is done
}

type phase int

const (
	confirming phase = iota
	removing
	finished
)

type item struct {
	row  row
	done bool
	err  error // why it was not removed, or a *engine.RecordLeftError
}

// removedMsg reports how removing item i went.
type removedMsg struct {
	i   int
	err error
}

func newRemoval(rows []*row, unchecked int) *removal {
	rm := &removal{unchecked: unchecked}
	for _, safe := range []bool{true, false} {
		for _, r := range rows {
			if r.safe == safe {
				rm.items = append(rm.items, &item{row: *r})
			}
		}
	}
	return rm
}

// force tells whether it is removed whatever it holds: the user was
// shown it is not safe and confirmed. One shown as safe is not forced,
// so work that appears in it after the scan stops its removal.
func (it *item) force() bool { return !it.row.safe }

// removed reports whether the worktree is gone from disk.
func (it *item) removed() bool {
	_, left := errors.AsType[*engine.RecordLeftError](it.err)
	return it.done && (it.err == nil || left)
}

func rowsOf(items []*item) []*row {
	out := make([]*row, len(items))
	for i, it := range items {
		out[i] = &it.row
	}
	return out
}

// view renders the confirmation and the progress: the worktrees and a
// line of key help, in height lines.
func (rm *removal) view(t theme, h help.Model, k keyMap, spin string, width, height int) string {
	verb := "Remove"
	if rm.phase == removing {
		verb = "Removing"
	}
	title := " " + t.title.Render(verb+" "+plural(len(rm.items), "worktree"))
	if size := totalSize(rowsOf(rm.items)); size != nil {
		title += t.subtle.Render(" · " + formatBytes(size))
	}
	note := "branches are kept"
	if rm.quitting {
		note = "stopping after this one…"
	}
	head := spread(title, t.subtle.Render(note)+" ", width)

	rows := rm.rows(t, spin, width)
	body := max(height-4, 1) // the title, the key help and a blank line after each
	if all := len(rows); all > body {
		// Keep the worktree being removed in sight.
		off := 0
		if rm.phase == removing {
			off = max(min(rm.focus()+2-body, all-body), 0)
		}
		rows = rows[off : off+body]
		if off+body < all {
			rows[len(rows)-1] = strings.Repeat(" ", rowIndent) + t.subtle.Render("…")
		}
	}

	var keys bindings
	if rm.phase == confirming {
		keys = bindings{k.confirm, k.back}
	}
	h.SetWidth(max(width-2, 0))
	return head + "\n\n" + strings.Join(rows, "\n") + "\n\n " + h.View(keys)
}

// focus is the line of the rows showing the item being removed.
func (rm *removal) focus() int {
	line := rm.next
	if rm.next < len(rm.items) && !rm.items[rm.next].row.safe && rm.next > 0 && rm.items[0].row.safe {
		line += 2 // the blank line and the heading of the ones not safe
	}
	return line
}

func (rm *removal) rows(t theme, spin string, width int) []string {
	names, notes := columns(rowsOf(rm.items))
	plain := lipgloss.NewStyle()
	var lines []string
	for i, it := range rm.items {
		if !it.row.safe && (i == 0 || it.row.safe != rm.items[i-1].row.safe) {
			if i > 0 {
				lines = append(lines, "")
			}
			lines = append(lines, strings.Repeat(" ", rowIndent)+
				t.failure.Render("not safe to delete · what they hold will be lost"))
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
		lines = append(lines, rowLine(t, &it.row, " "+mark+" ", plain, plain, names, notes, width))
	}
	if rm.unchecked > 0 {
		lines = append(lines, "", strings.Repeat(" ", rowIndent)+
			t.subtle.Render(plural(rm.unchecked, "worktree")+" still being checked left out"))
	}
	return lines
}

// summary tells what the removal came to, centered in width × height,
// with the keys that lead on from it.
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
		lines = append(lines, t.title.Render(formatBytes(size)+" freed"), t.subtle.Render(count))
	default: // some sizes were still being measured
		lines = append(lines, t.title.Render(count))
	}

	// Each list shows what fits, and how many more there are.
	room := height - len(lines) - 4
	list := func(title string, items []*item) {
		if len(items) == 0 || room < 3 {
			return
		}
		lines = append(lines, "", t.failure.Render(title))
		room -= 2
		for i, it := range items {
			if room == 1 && i < len(items)-1 {
				lines = append(lines, t.subtle.Render(fmt.Sprintf("+%d more", len(items)-i)))
				room--
				break
			}
			lines = append(lines, branchName(it.row.worktree)+"  "+t.subtle.Render(reason(it.err)))
			room--
		}
	}
	list(fmt.Sprintf("%d not removed", len(failed)), failed)
	list("git still lists "+plural(len(left), "removed worktree"), left)

	h.SetWidth(max(width-2, 0))
	lines = append(lines, "", "", h.View(bindings{k.rescan, k.back, k.quit}))
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, strings.Join(lines, "\n"))
}

// reason says why a worktree was not removed, in the list's words.
func reason(err error) string {
	if e, ok := errors.AsType[*engine.NotSafeError](err); ok {
		var why []string
		for _, n := range lopper.Notes(e.Worktree, e.Facts) {
			why = append(why, n.Text)
		}
		return "not safe anymore: " + strings.Join(why, noteSep)
	}
	return err.Error()
}
