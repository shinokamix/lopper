// Package verdict decides whether a worktree is safe to delete and words
// its facts. Only this package decides. The list and the CLI show its
// notes but never decide from them.
package verdict

import "github.com/shinokamix/lopper/internal/lopper"

// Safe reports whether deleting the worktree loses no work. That holds when
// git tracks it at its path, it is not locked, nothing in it is uncommitted
// or unchecked, and its commits are in the base branch. It also holds when
// the directory is already gone, since only git's record is left. An unknown
// fact never counts as clean, so a failed `git status` cannot make a
// worktree safe.
func Safe(wt lopper.Worktree, f lopper.Facts) bool {
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
