package tui

import (
	"strings"
	"testing"

	"github.com/shinokamix/lopper/internal/lopper"
	"github.com/shinokamix/lopper/internal/verdict"
)

func TestFormatBytes(t *testing.T) {
	cases := []struct {
		n    int64
		want string
	}{
		{0, "0 B"},
		{999, "999 B"},
		{1000, "1.0 kB"},
		{1500, "1.5 kB"},
		{1_000_000, "1.0 MB"},
		{1_400_000_000, "1.4 GB"},
		{2_000_000_000_000, "2.0 TB"},
	}
	for _, tc := range cases {
		if got := formatBytes(tc.n); got != tc.want {
			t.Errorf("formatBytes(%d) = %q, want %q", tc.n, got, tc.want)
		}
	}
}

// A row shows "merged" in its color whether or not the worktree is safe
// to delete, after the work deleting it would lose.
func TestFactsShowMergedInItsColor(t *testing.T) {
	merged, notMerged := new(lopper.MergedFF), new(lopper.NotMerged)
	cases := []struct {
		name   string
		row    row
		want   string
		marked bool // "merged" is in its color
	}{
		{"merged", row{checked: true, safe: true, facts: lopper.Facts{Dirty: new(0), UncheckedFiles: new(0), Unpushed: new(0), Merged: merged}}, "merged", true},
		{"merged but dirty", row{checked: true, facts: lopper.Facts{Dirty: new(1), UncheckedFiles: new(0), Unpushed: new(0), Merged: merged}}, "1 uncommitted · merged", true},
		{"merged but hidden files", row{checked: true, facts: lopper.Facts{Dirty: new(0), UncheckedFiles: new(2), Unpushed: new(0), Merged: merged}}, "2 unchecked · merged", true},
		{"merged but moved", row{checked: true, worktree: lopper.Worktree{State: lopper.StateMoved, MovedFrom: "/old"}, facts: lopper.Facts{Dirty: new(0), UncheckedFiles: new(0), Unpushed: new(0), Merged: merged}}, "moved by hand · merged", true},
		{"not merged and unchecked", row{checked: true, facts: lopper.Facts{Unpushed: new(0), Merged: notMerged}}, "couldn't check · not merged", false},
		{"folder gone", row{checked: true, safe: true, worktree: lopper.Worktree{State: lopper.StateGone}}, "folder gone", false},
		{"no facts yet", row{}, "checking…", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var texts []string
			marked := false
			for _, n := range facts(&tc.row) {
				texts = append(texts, n.Text)
				marked = marked || n.Kind == verdict.NoteMerged
			}
			if got := strings.Join(texts, " · "); got != tc.want || marked != tc.marked {
				t.Errorf("facts = %q (merged in color: %v), want %q (%v)", got, marked, tc.want, tc.marked)
			}
		})
	}
}
