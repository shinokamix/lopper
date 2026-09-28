package tui

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/help"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

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
	frame     int  // of the space freed counting up, once finished
	offset    int  // first visible line of the worktrees
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

// counted is how much of n the counter shows: all of it once done.
func (rm *removal) counted(n int64) int64 {
	if rm.frame >= frames {
		return n
	}
	left := 1 - float64(rm.frame)/frames
	return int64(float64(n) * (1 - left*left*left)) // ease out
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
	head := title
	if rm.quitting {
		head = spread(title, t.subtle.Render("stopping after this one…")+" ", width)
	}

	rows := rm.rows(t, spin, width)
	body := max(height-4, 1) // the title, the key help and a blank line after each
	all := len(rows)
	if all > body {
		// The user scrolls through what they confirm; the worktree being
		// removed stays in sight.
		off := rm.offset
		if rm.phase == removing {
			off = rm.focus() + 2 - body
		}
		off = max(min(off, all-body), 0)
		rm.offset = off
		rows = rows[off : off+body]
		more := strings.Repeat(" ", rowIndent) + t.subtle.Render("…")
		if off > 0 {
			rows[0] = more
		}
		if off+body < all {
			rows[len(rows)-1] = more
		}
	}

	var keys bindings
	if rm.phase == confirming {
		if all > body {
			keys = append(keys, k.scroll)
		}
		keys = append(keys, k.confirm, k.back)
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
	// Unlike the list, rows are not grouped by repository, and branch
	// names repeat across repositories: each row starts with its own.
	repoW := 0
	for _, it := range rm.items {
		repoW = max(repoW, ansi.StringWidth(repoLabel(it.row.worktree.Repo)))
	}
	repoW = min(repoW, width/5) // the branch says more: a long name gives way
	plain := lipgloss.NewStyle()
	var lines []string
	for i, it := range rm.items {
		if !it.row.safe && (i == 0 || it.row.safe != rm.items[i-1].row.safe) {
			if i > 0 {
				lines = append(lines, "")
			}
			// Level with the title, like repository headers in the list,
			// so it reads as a heading over the rows and not as one of them.
			lines = append(lines, " "+t.failure.Render("work in these will be lost"))
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
		lines = append(lines, rowLine(t, &it.row, lead, plain, plain, names, notes, width))
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
		// Padded to the width of the total, so the line stays put as the
		// counter goes through the units.
		total := formatBytes(size)
		now := fmt.Sprintf("%*s", len(total), formatBytes(new(rm.counted(*size))))
		lines = append(lines, t.title.Render(now+" freed"), t.subtle.Render(count))
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

// repoLabel names a worktree's repository in a row.
func repoLabel(repo lopper.Repo) string {
	if repo.Path == "" {
		return "unknown"
	}
	return repoName(repo.Path)
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
