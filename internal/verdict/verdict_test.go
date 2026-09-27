package verdict

import (
	"slices"
	"strings"
	"testing"

	"github.com/shinokamix/lopper/internal/lopper"
)

func TestEvaluate(t *testing.T) {
	n := func(v int) *int { return &v }
	m := func(k lopper.MergeKind) *lopper.MergeKind { return &k }
	repo := lopper.Worktree{Repo: lopper.Repo{DefaultBranch: "main"}}
	noBase := lopper.Worktree{}
	locked := repo
	locked.Locked = true

	cases := []struct {
		name      string
		wt        lopper.Worktree
		facts     lopper.Facts
		want      lopper.Level
		wantRules []string
	}{
		{"merged and clean", repo, lopper.Facts{Dirty: n(0), Unpushed: n(0), Merged: m(lopper.MergedFF)}, lopper.LevelSafe, []string{"merged"}},
		{"squash merged, local commits only", repo, lopper.Facts{Dirty: n(0), Unpushed: n(3), Merged: m(lopper.MergedSquash)}, lopper.LevelSafe, []string{"merged"}},
		{"merged but dirty", repo, lopper.Facts{Dirty: n(2), Unpushed: n(0), Merged: m(lopper.MergedFF)}, lopper.LevelKeep, []string{"dirty", "merged"}},
		{"unmerged and unpushed", repo, lopper.Facts{Dirty: n(0), Unpushed: n(1), Merged: m(lopper.NotMerged)}, lopper.LevelKeep, []string{"unpushed", "not-merged"}},
		{"unmerged but pushed", repo, lopper.Facts{Dirty: n(0), Unpushed: n(0), Merged: m(lopper.NotMerged)}, lopper.LevelReview, []string{"not-merged"}},
		{"locked", locked, lopper.Facts{Dirty: n(0), Unpushed: n(0), Merged: m(lopper.MergedFF)}, lopper.LevelKeep, []string{"locked", "merged"}},
		{"prunable", lopper.Worktree{Prunable: true}, lopper.Facts{}, lopper.LevelSafe, []string{"prunable"}},
		{"status failed but merged", repo, lopper.Facts{Unpushed: n(0), Merged: m(lopper.MergedFF), Errors: []string{"could not read status: boom"}}, lopper.LevelReview, []string{"incomplete", "merged"}},
		{"no base branch", noBase, lopper.Facts{Dirty: n(0), Unpushed: n(0)}, lopper.LevelReview, []string{"incomplete"}},
		{"nothing known", repo, lopper.Facts{}, lopper.LevelReview, []string{"incomplete"}},
		{"unknown facts never hide a keep", repo, lopper.Facts{Dirty: n(1)}, lopper.LevelKeep, []string{"incomplete", "dirty"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Evaluate(tc.wt, tc.facts)
			if got.Level != tc.want {
				t.Errorf("level = %v, want %v (reasons: %+v)", got.Level, tc.want, got.Reasons)
			}
			var rules []string
			for _, r := range got.Reasons {
				rules = append(rules, r.Rule)
			}
			if !slices.Equal(rules, tc.wantRules) {
				t.Errorf("rules = %q, want %q", rules, tc.wantRules)
			}
		})
	}
}

// TestIncompleteMessage checks that the reason names the missing facts and why.
func TestIncompleteMessage(t *testing.T) {
	n := func(v int) *int { return &v }

	f := lopper.Facts{Unpushed: n(0), Errors: []string{"could not read status: fatal: bad index"}}
	v := Evaluate(lopper.Worktree{}, f)
	if len(v.Reasons) != 1 || v.Reasons[0].Rule != "incomplete" {
		t.Fatalf("Evaluate = %+v, want one incomplete reason", v)
	}
	r := v.Reasons[0]
	for _, want := range []string{"uncommitted changes", "merge status", "no base branch found", "could not read status: fatal: bad index"} {
		if !strings.Contains(r.Message, want) {
			t.Errorf("message %q lacks %q", r.Message, want)
		}
	}
	if strings.Contains(r.Message, "unpushed") {
		t.Errorf("message %q mentions a known fact", r.Message)
	}
}
