package verdict

import (
	"testing"

	"github.com/shinokamix/lopper/internal/lopper"
)

func TestSafe(t *testing.T) {
	n := func(v int) *int { return &v }
	m := func(k lopper.MergeKind) *lopper.MergeKind { return &k }
	repo := lopper.Worktree{Repo: lopper.Repo{DefaultBranch: "main"}}
	noBase := lopper.Worktree{}
	locked := repo
	locked.Locked = true
	lockedGone := lopper.Worktree{Prunable: true, Locked: true}
	moved := repo
	moved.MovedFrom = "/old/wt"
	unconfirmed := repo
	unconfirmed.Unconfirmed = "git worktree list failed"
	clean := lopper.Facts{Dirty: n(0), Unpushed: n(0), Merged: m(lopper.MergedFF)}

	cases := []struct {
		name  string
		wt    lopper.Worktree
		facts lopper.Facts
		want  bool
	}{
		{"merged and clean", repo, clean, true},
		{"squash merged, local commits only", repo, lopper.Facts{Dirty: n(0), Unpushed: n(3), Merged: m(lopper.MergedSquash)}, true},
		{"merged but dirty", repo, lopper.Facts{Dirty: n(2), Unpushed: n(0), Merged: m(lopper.MergedFF)}, false},
		{"unmerged and unpushed", repo, lopper.Facts{Dirty: n(0), Unpushed: n(1), Merged: m(lopper.NotMerged)}, false},
		{"unmerged but pushed", repo, lopper.Facts{Dirty: n(0), Unpushed: n(0), Merged: m(lopper.NotMerged)}, false},
		{"locked", locked, clean, false},
		{"folder gone", lopper.Worktree{Prunable: true}, lopper.Facts{}, true},
		{"folder gone but locked", lockedGone, lopper.Facts{}, false},
		{"moved, merged and clean", moved, clean, false},
		{"unconfirmed, merged and clean", unconfirmed, clean, false},
		{"orphaned", lopper.Worktree{Orphaned: true}, lopper.Facts{SizeBytes: new(int64(4096))}, false},
		{"status failed but merged", repo, lopper.Facts{Unpushed: n(0), Merged: m(lopper.MergedFF), Errors: []string{"could not read status: boom"}}, false},
		{"unpushed unknown but merged", repo, lopper.Facts{Dirty: n(0), Merged: m(lopper.MergedFF)}, false},
		{"no base branch", noBase, lopper.Facts{Dirty: n(0), Unpushed: n(0)}, false},
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
