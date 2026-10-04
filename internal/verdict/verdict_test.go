package verdict

import (
	"slices"
	"testing"

	"github.com/shinokamix/lopper/internal/lopper"
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
			if got := Safe(tc.wt, tc.facts); got != tc.want {
				t.Errorf("Safe = %v, want %v", got, tc.want)
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
		want   []Note
	}{
		{lopper.MergedSquash, []Note{{"merged", NoteMerged}}},
		{lopper.NotMerged, []Note{{"3 unpushed", NoteWork}, {"not merged", NotePlain}}},
	} {
		f := lopper.Facts{Dirty: new(0), UncheckedFiles: new(0), Unpushed: new(3), Merged: new(tc.merged)}
		if got := Notes(repo, f); !slices.Equal(got, tc.want) {
			t.Errorf("Notes with 3 local commits, merged %s = %v, want %v", tc.merged, got, tc.want)
		}
	}
}
