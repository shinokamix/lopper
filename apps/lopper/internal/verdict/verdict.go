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

// notes describes a worktree by its facts, most pressing first: work that
// deleting it would lose, what git cannot tell about it, then how far the
// work got. A note that blocks says in its hint what to do about it.
func notes(wt lopper.Worktree, f lopper.Facts) []lopper.Note {
	var out []lopper.Note
	add := func(kind lopper.NoteKind, blocks bool, hint, format string, a ...any) {
		out = append(out, lopper.Note{Text: fmt.Sprintf(format, a...), Kind: kind, Blocks: blocks, Hint: hint})
	}
	base := wt.Repo.DefaultBranch
	detached := wt.Branch == ""
	merged := f.Merged != nil && *f.Merged != lopper.NotMerged
	if wt.Locked {
		add(lopper.NoteWork, true, "unlock it with `git -C <repository> worktree unlock <worktree>`", "locked")
	}
	if f.Dirty != nil && *f.Dirty > 0 {
		add(lopper.NoteWork, true, "commit or stash them", "%d uncommitted", *f.Dirty)
	}
	if f.UncheckedFiles != nil && *f.UncheckedFiles > 0 {
		add(lopper.NoteWork, true, "index flags hide their edits from git: "+
			"`git -C <worktree> ls-files -v` marks them with a lowercase letter or S. "+
			"`git -C <worktree> update-index --no-assume-unchanged -- <file>` and "+
			"`git -C <worktree> update-index --no-skip-worktree -- <file>` clear the flags for `git status`",
			"%d unchecked", *f.UncheckedFiles)
	}
	if f.Unpushed != nil && *f.Unpushed > 0 && !merged {
		hint := "push them with `git -C <worktree> push`, or `git -C <worktree> push -u <remote> <branch>` if the branch was never pushed"
		if detached {
			hint = "keep them on a branch with `git -C <worktree> switch -c <branch>`"
		}
		add(lopper.NoteWork, true, hint, "%d unpushed", *f.Unpushed)
	}
	inspected := true // whether inspect asks git about it
	switch wt.State {
	case lopper.StateTracked:
	case lopper.StateGone:
		add(lopper.NotePlain, false, "", "folder gone")
		inspected = false
	case lopper.StateOrphaned:
		add(lopper.NotePlain, true, "copy out what you need, then delete it with `lopper rm --force -- <worktree>`",
			"not tracked by git")
		inspected = false
	case lopper.StateMoved:
		add(lopper.NotePlain, true, "relink it with `git -C <repository> worktree repair <worktree>`", "moved by hand")
	case lopper.StateUnconfirmed:
		add(lopper.NotePlain, true, "try `git -C <repository> worktree repair <worktree>`", "not confirmed by git")
	}
	noBase := f.Merged == nil && base == ""
	if inspected && (f.Dirty == nil || f.UncheckedFiles == nil || f.Unpushed == nil || f.Merged == nil && !noBase) {
		add(lopper.NotePlain, true, "fix what git reported and try again", "couldn't check")
	}
	fetch := "run `git -C <worktree> fetch` if it was merged on the remote"
	switch {
	case merged:
		add(lopper.NoteMerged, false, "", "merged")
	case f.Merged != nil && detached:
		add(lopper.NotePlain, true, fetch, "not merged")
	case f.Merged != nil:
		add(lopper.NotePlain, true, "merge it into "+base+", or "+fetch, "not merged")
	case inspected && noBase:
		add(lopper.NotePlain, true, "lopper looks for origin/HEAD, main or master. "+
			"If origin is configured, run `git -C <repository> fetch origin`, then `git -C <repository> remote set-head origin --auto`. "+
			"Otherwise create main or master at the intended base", "no base branch")
	}
	return out
}
