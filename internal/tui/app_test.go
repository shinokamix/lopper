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
