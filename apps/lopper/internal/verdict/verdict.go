// Package verdict decides whether a worktree is safe to delete.
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

func safe(wt lopper.Worktree, f lopper.Facts) bool {
	if wt.Locked {
		return false
	}
	switch wt.State {
	case lopper.StateGone:
		return true
	case lopper.StateTracked:
		return f.Dirty != nil && *f.Dirty == 0 &&
			f.UncheckedFiles != nil && *f.UncheckedFiles == 0 &&
			f.Unpushed != nil &&
			f.Merged != nil && *f.Merged != lopper.NotMerged
	case lopper.StateOrphaned, lopper.StateMoved, lopper.StateUnconfirmed:
	}
	return false
}

const uncheckedHint = "index flags hide their edits from git: `git ls-files -v | grep '^[a-zS]'` lists them, " +
	"`git update-index --no-assume-unchanged -- <file>` and `git update-index --no-skip-worktree -- <file>` " +
	"clear them for `git status`"

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
	inspected := true
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
