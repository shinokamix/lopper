package verdict

import (
	"fmt"

	"github.com/shinokamix/lopper/internal/lopper"
)

// Note is one fact about a worktree, worded for the list and the CLI.
type Note struct {
	Text string
	Kind NoteKind
}

// NoteKind says what a note is about, so the list can color it.
type NoteKind int

// The kinds of note.
const (
	NotePlain  NoteKind = iota
	NoteWork            // work or unchecked files that deletion puts at risk
	NoteMerged          // the work is in the base branch
)

// Notes describes a worktree by its facts, most pressing first: work
// that deleting it would lose, what git cannot tell about it, then how far
// the work got.
func Notes(wt lopper.Worktree, f lopper.Facts) []Note {
	var out []Note
	add := func(kind NoteKind, format string, a ...any) {
		out = append(out, Note{fmt.Sprintf(format, a...), kind})
	}
	merged := f.Merged != nil && *f.Merged != lopper.NotMerged
	if wt.Locked {
		add(NoteWork, "locked")
	}
	if f.Dirty != nil && *f.Dirty > 0 {
		add(NoteWork, "%d uncommitted", *f.Dirty)
	}
	if f.UncheckedFiles != nil && *f.UncheckedFiles > 0 {
		add(NoteWork, "%d unchecked", *f.UncheckedFiles)
	}
	if f.Unpushed != nil && *f.Unpushed > 0 && !merged {
		add(NoteWork, "%d unpushed", *f.Unpushed)
	}
	inspected := true // whether inspect asks git about it
	switch wt.State {
	case lopper.StateTracked:
	case lopper.StateGone:
		add(NotePlain, "folder gone")
		inspected = false
	case lopper.StateOrphaned:
		add(NotePlain, "not tracked by git")
		inspected = false
	case lopper.StateMoved:
		add(NotePlain, "moved by hand")
	case lopper.StateUnconfirmed:
		add(NotePlain, "not confirmed by git")
	}
	if inspected && (f.Dirty == nil || f.UncheckedFiles == nil || f.Unpushed == nil || f.Merged == nil) {
		add(NotePlain, "couldn't check")
	}
	switch {
	case merged:
		add(NoteMerged, "merged")
	case f.Merged != nil:
		add(NotePlain, "not merged")
	}
	return out
}
