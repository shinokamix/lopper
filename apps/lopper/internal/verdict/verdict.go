// Package verdict decides whether a worktree is safe to delete and words
// its facts. Only this package decides. The list and the CLI show its
// verdict but never decide from the facts themselves.
package verdict

import (
	"fmt"
	"strings"

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
	// git worktree commands run in the repository, since git may not work
	// inside a worktree it lost track of.
	repo, path := wt.Repo.Path, wt.Path
	base := wt.Repo.DefaultBranch
	detached := wt.Branch == ""
	merged := f.Merged != nil && *f.Merged != lopper.NotMerged
	if wt.Locked {
		add(lopper.NoteWork, true, "unlock it with "+git(repo, "worktree unlock", lopper.Quote(path)), "locked")
	}
	if f.Dirty != nil && *f.Dirty > 0 {
		add(lopper.NoteWork, true, "commit or stash them", "%d uncommitted", *f.Dirty)
	}
	if f.UncheckedFiles != nil && *f.UncheckedFiles > 0 {
		add(lopper.NoteWork, true, "index flags hide their edits from git: "+
			git(path, "ls-files -v | grep '^[a-zS]'")+" lists them, "+
			// One update-index call clears only one of the two flags.
			git(path, "update-index --no-assume-unchanged -- <file>")+" and "+
			git(path, "update-index --no-skip-worktree -- <file>")+" clear them for `git status`",
			"%d unchecked", *f.UncheckedFiles)
	}
	if f.Unpushed != nil && *f.Unpushed > 0 && !merged {
		hint := "push them with " + git(path, "push") + ", or " + git(path, "push -u origin", lopper.Quote(wt.Branch)) +
			" if the branch was never pushed"
		if detached {
			hint = "keep them on a branch with " + git(path, "switch -c <branch>")
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
		add(lopper.NotePlain, true, "copy out what you need, then delete it with `lopper rm --force "+lopper.Quote(path)+"`",
			"not tracked by git")
		inspected = false
	case lopper.StateMoved:
		add(lopper.NotePlain, true, "relink it with "+git(repo, "worktree repair", lopper.Quote(path)), "moved by hand")
	case lopper.StateUnconfirmed:
		add(lopper.NotePlain, true, "try "+git(repo, "worktree repair", lopper.Quote(path)), "not confirmed by git")
	}
	noBase := f.Merged == nil && base == ""
	if inspected && (f.Dirty == nil || f.UncheckedFiles == nil || f.Unpushed == nil || f.Merged == nil && !noBase) {
		add(lopper.NotePlain, true, "fix what git reported and try again", "couldn't check")
	}
	fetch := "run " + git(path, "fetch") + " if it was merged on the remote"
	switch {
	case merged:
		add(lopper.NoteMerged, false, "", "merged")
	case f.Merged != nil && detached:
		add(lopper.NotePlain, true, fetch, "not merged")
	case f.Merged != nil:
		add(lopper.NotePlain, true, "merge it into "+base+", or "+fetch, "not merged")
	case inspected && noBase:
		add(lopper.NotePlain, true, "set one with "+git(repo, "remote set-head origin --auto"), "no base branch")
	}
	return out
}

// git words a git command that runs in dir, since lopper runs from anywhere
// and a pasted command must not act on the repository the shell is in.
func git(dir string, args ...string) string {
	return "`git -C " + lopper.Quote(dir) + " " + strings.Join(args, " ") + "`"
}
