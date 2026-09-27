package tui

import (
	"errors"
	"strings"
	"testing"

	"charm.land/bubbles/v2/help"
	tea "charm.land/bubbletea/v2"

	"github.com/shinokamix/lopper/internal/engine"
	"github.com/shinokamix/lopper/internal/lopper"
)

func testApp() *app {
	return &app{store: newStore(), keys: defaultKeys(), theme: newTheme(true), help: help.New(), list: newList()}
}

func TestScanErrorVisible(t *testing.T) {
	for _, name := range []string{"empty", "partial results"} {
		t.Run(name, func(t *testing.T) {
			a := testApp()
			a.Update(tea.WindowSizeMsg{Width: 100, Height: 20})
			if name == "partial results" {
				a.Update(eventMsg{engine.WorktreeFound{Worktree: lopper.Worktree{ID: "wt", Path: "/repo/wt", Branch: "feature"}}})
			}
			a.Update(eventMsg{engine.ScanDone{Err: errors.New("scan /restricted: permission denied")}})
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

func TestNavigationKeepsSelectionVisible(t *testing.T) {
	a := testApp()
	a.Update(tea.WindowSizeMsg{Width: 100, Height: 8})
	for _, branch := range []string{"first", "second", "third"} {
		a.Update(eventMsg{engine.WorktreeFound{Worktree: lopper.Worktree{ID: lopper.ID(branch), Branch: branch}}})
	}
	a.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	a.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	a.Update(tea.KeyPressMsg{Code: ' '})
	view := a.View().Content
	if !strings.Contains(view, "third") || !strings.Contains(view, "✓") || strings.Contains(view, "first") {
		t.Errorf("selected third row is not visible: %s", view)
	}
	a.Update(tea.KeyPressMsg{Code: tea.KeyUp})
	a.Update(tea.KeyPressMsg{Code: tea.KeyUp})
	view = a.View().Content
	if !strings.Contains(view, "first") || strings.Contains(view, "third") {
		t.Errorf("returning to first row did not scroll up: %s", view)
	}
	a.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	a.Update(tea.WindowSizeMsg{Width: 100, Height: 7})
	view = a.View().Content
	if !strings.Contains(view, "second") || strings.Contains(view, "first") || strings.Contains(view, "third") {
		t.Errorf("resize hid the current row: %s", view)
	}
}
