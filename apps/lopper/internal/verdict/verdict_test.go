package verdict

import (
	"slices"
	"strings"
	"testing"

	"github.com/shinokamix/lopper/apps/lopper/internal/lopper"
)

func TestSafe(t *testing.T) {
	repo := lopper.Worktree{Repo: lopper.Repo{DefaultBranch: "main"}}
	noBase := lopper.Worktree{}
	locked := repo
	locked.Locked = true
	lockedGone := lopper.Worktree{State: lopper.StateGone, Locked: true}
	moved := repo
	moved.State, moved.MovedFrom = lopper.StateMoved, "/old/wt"
	unconfirmed := repo
	unconfirmed.State, unconfirmed.Reason = lopper.StateUnconfirmed, "git worktree list failed"
	clean := lopper.Facts{Dirty: new(0), UncheckedFiles: new(0), Unpushed: new(0), Merged: new(lopper.MergedFF)}

	cases := []struct {
		name  string
		wt    lopper.Worktree
		facts lopper.Facts
		want  bool
	}{
		{"merged and clean", repo, clean, true},
		{"index flags hide files", repo, lopper.Facts{Dirty: new(0), UncheckedFiles: new(2), Unpushed: new(0), Merged: new(lopper.MergedFF)}, false},
		{"index flags unknown", repo, lopper.Facts{Dirty: new(0), Unpushed: new(0), Merged: new(lopper.MergedFF)}, false},
		{"squash merged, local commits only", repo, lopper.Facts{Dirty: new(0), UncheckedFiles: new(0), Unpushed: new(3), Merged: new(lopper.MergedSquash)}, true},
		{"merged but dirty", repo, lopper.Facts{Dirty: new(2), UncheckedFiles: new(0), Unpushed: new(0), Merged: new(lopper.MergedFF)}, false},
		{"unmerged and unpushed", repo, lopper.Facts{Dirty: new(0), UncheckedFiles: new(0), Unpushed: new(1), Merged: new(lopper.NotMerged)}, false},
		{"unmerged but pushed", repo, lopper.Facts{Dirty: new(0), UncheckedFiles: new(0), Unpushed: new(0), Merged: new(lopper.NotMerged)}, false},
		{"locked", locked, clean, false},
		{"folder gone", lopper.Worktree{State: lopper.StateGone}, lopper.Facts{}, true},
		{"folder gone but locked", lockedGone, lopper.Facts{}, false},
		{"moved, merged and clean", moved, clean, false},
		{"unconfirmed, merged and clean", unconfirmed, clean, false},
		{"orphaned", lopper.Worktree{State: lopper.StateOrphaned}, lopper.Facts{SizeBytes: new(int64(4096))}, false},
		{"status failed but merged", repo, lopper.Facts{UncheckedFiles: new(0), Unpushed: new(0), Merged: new(lopper.MergedFF), Errors: []string{"could not read status: boom"}}, false},
		{"unpushed unknown but merged", repo, lopper.Facts{Dirty: new(0), UncheckedFiles: new(0), Merged: new(lopper.MergedFF)}, false},
		{"no base branch", noBase, lopper.Facts{Dirty: new(0), UncheckedFiles: new(0), Unpushed: new(0)}, false},
		{"nothing known", repo, lopper.Facts{}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := Of(tc.wt, tc.facts)
			if v.Safe != tc.want {
				t.Errorf("Safe = %v, want %v", v.Safe, tc.want)
			}
			// A refusal the user cannot see the reason for reads as a bug.
			if !v.Safe && !slices.ContainsFunc(v.Notes, func(n lopper.Note) bool { return n.Blocks }) {
				t.Errorf("not safe, but no note says why: %v", v.Notes)
			}
		})
	}
}

// After a squash merge, local commits exist nowhere else. Counting them as
// unpushed would warn about work that is not at risk.
func TestNotesCountUnpushedOnlyUntilMerged(t *testing.T) {
	repo := lopper.Worktree{Repo: lopper.Repo{DefaultBranch: "main"}}
	for _, tc := range []struct {
		merged lopper.MergeKind
		want   []lopper.Note
	}{
		{lopper.MergedSquash, []lopper.Note{{Text: "merged", Kind: lopper.NoteMerged}}},
		{lopper.NotMerged, []lopper.Note{{Text: "3 unpushed", Kind: lopper.NoteWork, Blocks: true}, {Text: "not merged", Kind: lopper.NotePlain, Blocks: true}}},
	} {
		f := lopper.Facts{Dirty: new(0), UncheckedFiles: new(0), Unpushed: new(3), Merged: new(tc.merged)}
		if got := Of(repo, f).Notes; !slices.Equal(got, tc.want) {
			t.Errorf("Notes with 3 local commits, merged %s = %v, want %v", tc.merged, got, tc.want)
		}
	}
}

// Without a base branch nothing failed: lopper has nothing to compare the
// branch with, and saying "couldn't check" would send the user hunting for
// an error.
func TestNoBaseBranchIsNotAFailedCheck(t *testing.T) {
	f := lopper.Facts{Dirty: new(0), UncheckedFiles: new(0), Unpushed: new(0)}
	want := []lopper.Note{{Text: "no base branch", Kind: lopper.NotePlain, Blocks: true}}
	if got := Of(lopper.Worktree{}, f).Notes; !slices.Equal(got, want) {
		t.Errorf("Notes without a base branch = %v, want %v", got, want)
	}
}

// "merged" keeps its kind, and so its color, whether or not the worktree is
// safe to delete, and comes after the work deleting it would lose.
func TestNotesShowMergedLastInItsKind(t *testing.T) {
	repo := lopper.Worktree{Repo: lopper.Repo{DefaultBranch: "main"}}
	moved := repo
	moved.State, moved.MovedFrom = lopper.StateMoved, "/old"
	merged, notMerged := new(lopper.MergedFF), new(lopper.NotMerged)
	cases := []struct {
		name   string
		wt     lopper.Worktree
		facts  lopper.Facts
		want   string
		marked bool // "merged" is of kind NoteMerged
	}{
		{"merged", repo, lopper.Facts{Dirty: new(0), UncheckedFiles: new(0), Unpushed: new(0), Merged: merged}, "merged", true},
		{"merged but dirty", repo, lopper.Facts{Dirty: new(1), UncheckedFiles: new(0), Unpushed: new(0), Merged: merged}, "1 uncommitted · merged", true},
		{"merged but hidden files", repo, lopper.Facts{Dirty: new(0), UncheckedFiles: new(2), Unpushed: new(0), Merged: merged}, "2 unchecked · merged", true},
		{"merged but moved", moved, lopper.Facts{Dirty: new(0), UncheckedFiles: new(0), Unpushed: new(0), Merged: merged}, "moved by hand · merged", true},
		{"not merged and unchecked", repo, lopper.Facts{Unpushed: new(0), Merged: notMerged}, "couldn't check · not merged", false},
		{"folder gone", lopper.Worktree{State: lopper.StateGone}, lopper.Facts{}, "folder gone", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var texts []string
			marked := false
			for _, n := range Of(tc.wt, tc.facts).Notes {
				texts = append(texts, n.Text)
				marked = marked || n.Kind == lopper.NoteMerged
			}
			if got := strings.Join(texts, " · "); got != tc.want || marked != tc.marked {
				t.Errorf("notes = %q (merged of its kind: %v), want %q (%v)", got, marked, tc.want, tc.marked)
			}
		})
	}
}
