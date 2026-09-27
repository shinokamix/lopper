// Package verdict turns Facts into a recommendation. Everything here is
// a pure function of its inputs, so rules are trivially unit-testable.
package verdict

import (
	"fmt"
	"strings"

	"github.com/shinokamix/lopper/internal/lopper"
)

// Evaluate applies rules and picks the strictest level among the reasons.
func Evaluate(wt lopper.Worktree, f lopper.Facts) lopper.Verdict {
	rules := [...]func(lopper.Worktree, lopper.Facts) *lopper.Reason{
		prunable, orphaned, moved, unconfirmed, locked, incomplete, dirty, unpushed, merged, notMerged,
	}
	var v lopper.Verdict
	for _, rule := range rules {
		if r := rule(wt, f); r != nil {
			v.Reasons = append(v.Reasons, *r)
			if r.Level > v.Level {
				v.Level = r.Level
			}
		}
	}
	return v
}

func prunable(wt lopper.Worktree, _ lopper.Facts) *lopper.Reason {
	if !wt.Prunable {
		return nil
	}
	return &lopper.Reason{Rule: "prunable", Level: lopper.LevelSafe, Message: "directory is already gone"}
}

// orphaned is never safe: without git there is no way to tell whether the
// files hold work that exists nowhere else.
func orphaned(wt lopper.Worktree, _ lopper.Facts) *lopper.Reason {
	if !wt.Orphaned {
		return nil
	}
	return &lopper.Reason{
		Rule: "orphaned", Level: lopper.LevelReview,
		Message: fmt.Sprintf("not tracked by %s anymore: git cannot check it for unsaved work", wt.Repo.Path),
	}
}

// moved is never safe: git cannot remove the worktree where it is now,
// and removing it by hand leaves git a record of it at the old path.
func moved(wt lopper.Worktree, _ lopper.Facts) *lopper.Reason {
	if wt.MovedFrom == "" {
		return nil
	}
	return &lopper.Reason{
		Rule: "moved", Level: lopper.LevelReview,
		Message: fmt.Sprintf("moved from %s: run `git worktree repair` in it", wt.MovedFrom),
	}
}

// unconfirmed is never safe: even when git works inside the directory,
// its repository may not know about it, or know it under another name.
func unconfirmed(wt lopper.Worktree, _ lopper.Facts) *lopper.Reason {
	if wt.Unconfirmed == "" {
		return nil
	}
	return &lopper.Reason{
		Rule: "unconfirmed", Level: lopper.LevelReview,
		Message: fmt.Sprintf("%s cannot confirm this worktree: %s", wt.Repo.Path, wt.Unconfirmed),
	}
}

func locked(wt lopper.Worktree, _ lopper.Facts) *lopper.Reason {
	if !wt.Locked {
		return nil
	}
	return &lopper.Reason{Rule: "locked", Level: lopper.LevelKeep, Message: "locked with git worktree lock"}
}

// incomplete enforces the core invariant: a worktree that still exists is
// never safe unless every safety-critical fact is known. Without it a failed
// `git status` would silently skip the dirty rule and let merged say "safe".
func incomplete(wt lopper.Worktree, f lopper.Facts) *lopper.Reason {
	if wt.Prunable || wt.Orphaned { // no git facts to expect; their own rules explain why
		return nil
	}
	var unknown []string
	if f.Dirty == nil {
		unknown = append(unknown, "uncommitted changes")
	}
	if f.Unpushed == nil {
		unknown = append(unknown, "unpushed commits")
	}
	if f.Merged == nil {
		unknown = append(unknown, "merge status")
	}
	if len(unknown) == 0 {
		return nil
	}
	why := f.Errors
	if f.Merged == nil && wt.Repo.DefaultBranch == "" {
		why = append([]string{"no base branch found"}, why...)
	}
	msg := "unknown: " + strings.Join(unknown, ", ")
	if len(why) > 0 {
		msg += " (" + strings.Join(why, "; ") + ")"
	}
	return &lopper.Reason{Rule: "incomplete", Level: lopper.LevelReview, Message: msg}
}

func dirty(_ lopper.Worktree, f lopper.Facts) *lopper.Reason {
	if f.Dirty == nil || *f.Dirty == 0 {
		return nil
	}
	return &lopper.Reason{
		Rule: "dirty", Level: lopper.LevelKeep,
		Message: fmt.Sprintf("%d uncommitted change(s)", *f.Dirty),
	}
}

// unpushed only matters for unmerged work: after a squash merge the
// original commits legitimately exist nowhere else.
func unpushed(_ lopper.Worktree, f lopper.Facts) *lopper.Reason {
	if f.Unpushed == nil || *f.Unpushed == 0 || isMerged(f) {
		return nil
	}
	return &lopper.Reason{
		Rule: "unpushed", Level: lopper.LevelKeep,
		Message: fmt.Sprintf("%d commit(s) not on any remote", *f.Unpushed),
	}
}

func merged(wt lopper.Worktree, f lopper.Facts) *lopper.Reason {
	if !isMerged(f) {
		return nil
	}
	return &lopper.Reason{
		Rule: "merged", Level: lopper.LevelSafe,
		Message: fmt.Sprintf("work is in %s (%s)", wt.Repo.DefaultBranch, *f.Merged),
	}
}

func notMerged(_ lopper.Worktree, f lopper.Facts) *lopper.Reason {
	if f.Merged == nil || *f.Merged != lopper.NotMerged {
		return nil
	}
	return &lopper.Reason{Rule: "not-merged", Level: lopper.LevelReview, Message: "work not found in base branch"}
}

func isMerged(f lopper.Facts) bool {
	return f.Merged != nil && *f.Merged != lopper.NotMerged
}
