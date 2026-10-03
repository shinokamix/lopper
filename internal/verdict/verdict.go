// Package verdict is what lopper concludes from the facts about a
// worktree: whether it is safe to delete, and those facts in words. It is
// the only place that decides: the list and the CLI show its notes and
// never decide from them. Safe and Notes are pure functions of their
// inputs, so they are trivially unit-testable.
package verdict

import "github.com/shinokamix/lopper/internal/lopper"

// Safe reports whether deleting the worktree loses no work: git tracks it
// where it is, it is not locked, nothing in it is uncommitted or unchecked,
// and its commits are in the base branch. A worktree whose directory is already
// gone is safe: only git's record of it is left. A fact that is not known
// never counts as clean, so a failed `git status` cannot make a worktree
// safe.
func Safe(wt lopper.Worktree, f lopper.Facts) bool {
	if wt.Locked { // the user asked git to keep it
		return false
	}
	switch wt.State {
	case lopper.StateGone: // only git's record of it is left
		return true
	case lopper.StateTracked:
		// Once merged, unpushed commits are not lost: after a squash merge
		// the original commits legitimately exist nowhere else.
		return f.Dirty != nil && *f.Dirty == 0 &&
			f.UncheckedFiles != nil && *f.UncheckedFiles == 0 &&
			f.Unpushed != nil &&
			f.Merged != nil && *f.Merged != lopper.NotMerged
	case lopper.StateOrphaned, lopper.StateMoved, lopper.StateUnconfirmed: // git cannot check it
	}
	return false
}
