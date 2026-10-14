// Package verdict decides whether a worktree is safe to delete and words
// its facts. Only this package decides. The list and the CLI show its
// verdict but never decide from the facts themselves.
package verdict

import (
	"fmt"

	"github.com/shinokamix/lopper/apps/lopper/internal/lopper"
)

// Of concludes what deleting the worktree would cost. A verdict that is not
// safe always has a note that blocks, so no UI can refuse without saying why.
func Of(wt lopper.Worktree, f lopper.Facts) lopper.Verdict {
	notes := notes(wt, f)
	safe := safe(wt, f)
	for _, n := range notes {
		safe = safe && !n.Blocks
	}
	return lopper.Verdict{Safe: safe, Notes: notes}
}

// safe reports whether deleting the worktree loses no work. That holds when
// git tracks it at its path, it is not locked, nothing in it is uncommitted
// or unchecked, and its commits are in the base branch. It also holds when
// the directory is already gone, since only git's record is left. An unknown
// fact never counts as clean, so a failed `git status` cannot make a
// worktree safe.
func safe(wt lopper.Worktree, f lopper.Facts) bool {
	if wt.Locked { // the user asked git to keep it
		return false
	}
	switch wt.State {
	case lopper.StateGone:
		return true
	case lopper.StateTracked:
		// After a squash merge the original commits exist nowhere else, so
		// unpushed commits of merged work are not lost.
		return f.Dirty != nil && *f.Dirty == 0 &&
			f.UncheckedFiles != nil && *f.UncheckedFiles == 0 &&
			f.Unpushed != nil &&
			f.Merged != nil && *f.Merged != lopper.NotMerged
	case lopper.StateOrphaned, lopper.StateMoved, lopper.StateUnconfirmed: // git cannot check it
	}
	return false
}

// uncheckedHint is how to list files whose index flags hide edits and how to
// clear the flags so `git status` sees the edits again.
const uncheckedHint = "index flags hide their edits from git: `git ls-files -v | grep '^[a-zS]'` lists them, " +
	"`git update-index --no-assume-unchanged -- <file>` and `git update-index --no-skip-worktree -- <file>` " +
	"clear them for `git status`"

// notes describes a worktree by its facts, most pressing first: work that
// deleting it would lose, what git cannot tell about it, then how far the
// work got.
func notes(wt lopper.Worktree, f lopper.Facts) []lopper.Note {
	var out []lopper.Note
	add := func(kind lopper.NoteKind, blocks bool, format string, a ...any) *lopper.Note {
		out = append(out, lopper.Note{Text: fmt.Sprintf(format, a...), Kind: kind, Blocks: blocks})
		return &out[len(out)-1]
	}
	merged := f.Merged != nil && *f.Merged != lopper.NotMerged
	if wt.Locked {
		add(lopper.NoteWork, true, "locked")
	}
	if f.Dirty != nil && *f.Dirty > 0 {
		add(lopper.NoteWork, true, "%d uncommitted", *f.Dirty)
	}
	if f.UncheckedFiles != nil && *f.UncheckedFiles > 0 {
		add(lopper.NoteWork, true, "%d unchecked", *f.UncheckedFiles).Hint = uncheckedHint
	}
	if f.Unpushed != nil && *f.Unpushed > 0 && !merged {
		add(lopper.NoteWork, true, "%d unpushed", *f.Unpushed)
	}
	inspected := true // whether inspect asks git about it
	switch wt.State {
	case lopper.StateTracked:
	case lopper.StateGone:
		add(lopper.NotePlain, false, "folder gone")
		inspected = false
	case lopper.StateOrphaned:
		add(lopper.NotePlain, true, "not tracked by git")
		inspected = false
	case lopper.StateMoved:
		add(lopper.NotePlain, true, "moved by hand")
	case lopper.StateUnconfirmed:
		add(lopper.NotePlain, true, "not confirmed by git")
	}
	noBase := f.Merged == nil && wt.Repo.DefaultBranch == ""
	if inspected && (f.Dirty == nil || f.UncheckedFiles == nil || f.Unpushed == nil || f.Merged == nil && !noBase) {
		add(lopper.NotePlain, true, "couldn't check")
	}
	switch {
	case merged:
		add(lopper.NoteMerged, false, "merged")
	case f.Merged != nil:
		add(lopper.NotePlain, true, "not merged")
	case inspected && noBase:
		add(lopper.NotePlain, true, "no base branch")
	}
	return out
}
