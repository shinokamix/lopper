package tui

import (
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/shinokamix/lopper/internal/engine"
	"github.com/shinokamix/lopper/internal/lopper"
)

func TestScanErrorVisible(t *testing.T) {
	for _, name := range []string{"empty", "partial results"} {
		t.Run(name, func(t *testing.T) {
			a := testApp()
			a.Update(tea.WindowSizeMsg{Width: 100, Height: 20})
			if name == "partial results" {
				a.Update(eventMsg{ev: engine.WorktreeFound{Worktree: lopper.Worktree{ID: "wt", Path: "/repo/wt", Branch: "feature"}}})
			}
			a.Update(eventMsg{ev: engine.ScanDone{Err: errors.New("scan /restricted: permission denied")}})
			view := a.View().Content
			if !strings.Contains(view, "scan /restricted: permission denied") || strings.Contains(view, "done") || strings.Contains(view, "scanning…") {
				t.Errorf("scan error is not visible as a failure: %s", view)
			}
			if name == "partial results" && !strings.Contains(view, "/repo/wt") {
				t.Errorf("scan error hid partial results: %s", view)
			}
		})
	}
}

// Until the repositories an earlier scan met are listed, the status says
// so: the worktrees shown are the known ones, and new ones may follow.
func TestStatusTellsKnownRepositoriesFromTheSearch(t *testing.T) {
	a := testApp()
	a.Update(tea.WindowSizeMsg{Width: 100, Height: 20})
	a.Update(eventMsg{ev: engine.WorktreeFound{Worktree: lopper.Worktree{ID: "wt", Path: "/repo/wt", Branch: "feature"}}})
	if view := a.View().Content; !strings.Contains(view, "checking known repositories · 1 found") {
		t.Errorf("status before the known repositories are listed:\n%s", view)
	}
	a.Update(eventMsg{ev: engine.KnownListed{}})
	if view := a.View().Content; !strings.Contains(view, "scanning · 1 found") || strings.Contains(view, "known repositories") {
		t.Errorf("status once the known repositories are listed:\n%s", view)
	}
}
