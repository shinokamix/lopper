package tui

import (
	"context"
	"slices"
	"strings"
	"testing"

	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"

	"github.com/shinokamix/lopper/internal/engine"
	"github.com/shinokamix/lopper/internal/lopper"
)

// remover stands in for Engine.Remove: it records what it was asked to
// remove and fails with fail's error for a worktree there.
type remover struct {
	calls []string // "id" or "id forced"
	fail  map[lopper.ID]error
}

func (r *remover) remove(_ context.Context, wt lopper.Worktree, force bool) error {
	call := string(wt.ID)
	if force {
		call += " forced"
	}
	r.calls = append(r.calls, call)
	return r.fail[wt.ID]
}

func removalApp(t *testing.T) (*app, *remover) {
	a := testApp()
	rm := &remover{}
	a.ctx, a.remove = t.Context(), rm.remove
	a.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	return a, rm
}

// found reports a checked worktree of repository /r with its size.
func found(a *app, id string, size int64, safe bool, f lopper.Facts) {
	a.Update(eventMsg{ev: engine.WorktreeFound{Worktree: lopper.Worktree{
		ID: lopper.ID(id), Path: "/r/" + id, Branch: id, Repo: lopper.Repo{Path: "/r"},
	}}})
	f.SizeBytes = &size
	a.Update(eventMsg{ev: engine.FactsUpdated{ID: lopper.ID(id), Facts: f, Safe: safe}})
}

// press sends a key and runs what it leads to, as the program would.
func press(a *app, code rune) tea.Cmd {
	_, cmd := a.Update(tea.KeyPressMsg{Code: code})
	return cmd
}

// settle runs cmd and every command the messages it leads to return, as
// the program would, apart from the spinner's ticks.
func settle(a *app, cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	switch msg := cmd().(type) {
	case tea.BatchMsg:
		for _, c := range msg {
			settle(a, c)
		}
	case spinner.TickMsg, nil:
	default:
		_, next := a.Update(msg)
		settle(a, next)
	}
}

func view(a *app) string { return strings.Join(plainLines(a), "\n") }

// Removing asks once, showing what would be lost, and then removes every
// selected worktree: a safe one without force, so git still guards it,
// and one shown as not safe with force, since the user confirmed it.
func TestRemoveAsksOnceThenRemovesSelected(t *testing.T) {
	a, rm := removalApp(t)
	found(a, "clean", 2_000_000_000, true, lopper.Facts{Merged: new(lopper.MergedFF)})
	found(a, "wip", 1_000_000_000, false, lopper.Facts{Dirty: new(3)})
	found(a, "other", 100, true, lopper.Facts{})
	press(a, ' ') // clean, then the cursor moves to wip
	press(a, ' ')

	press(a, 'd')
	press(a, tea.KeyEscape)
	press(a, 'd')
	v := view(a)
	if !strings.Contains(v, "Remove 2 worktrees · 3.0 GB") || strings.Contains(v, "other") {
		t.Errorf("confirmation does not show the two selected worktrees:\n%s", v)
	}
	if lines := plainLines(a); lineWith(t, lines, "not safe to delete") > lineWith(t, lines, "wip") ||
		!strings.Contains(lines[lineWith(t, lines, "wip")], "3 uncommitted") {
		t.Errorf("worktree that is not safe is not shown under the warning with what it holds:\n%s", v)
	}
	if len(rm.calls) > 0 {
		t.Fatalf("removed before confirmation: %v", rm.calls)
	}

	settle(a, press(a, tea.KeyEnter))
	if want := []string{"clean", "wip forced"}; !slices.Equal(rm.calls, want) {
		t.Errorf("removed %v, want %v", rm.calls, want)
	}
	if v := view(a); !strings.Contains(v, "3.0 GB freed") || !strings.Contains(v, "2 worktrees removed") ||
		strings.Contains(v, "not removed") {
		t.Errorf("summary does not tell what was removed:\n%s", v)
	}

	press(a, tea.KeyEscape)
	if v := view(a); strings.Contains(v, "clean") || strings.Contains(v, "wip") || !strings.Contains(v, "other") ||
		strings.Contains(v, "selected") {
		t.Errorf("list still shows removed worktrees, or lost the rest:\n%s", v)
	}
}

// Without a selection, the remove key takes the row under the cursor. A
// worktree that was safe when confirmed but holds work by the time it is
// removed stays, and the summary says why.
func TestRemoveCursorRowKeepsOneThatGotWork(t *testing.T) {
	a, rm := removalApp(t)
	found(a, "big", 2000, true, lopper.Facts{})
	found(a, "busy", 1000, true, lopper.Facts{})
	rm.fail = map[lopper.ID]error{"busy": &engine.NotSafeError{
		Worktree: lopper.Worktree{ID: "busy"}, Facts: lopper.Facts{Dirty: new(1)},
	}}
	press(a, tea.KeyDown)

	press(a, 'd')
	settle(a, press(a, tea.KeyEnter))
	if want := []string{"busy"}; !slices.Equal(rm.calls, want) {
		t.Errorf("removed %v, want only the row under the cursor, not forced: %v", rm.calls, want)
	}
	if v := view(a); !strings.Contains(v, "nothing removed") || !strings.Contains(v, "1 not removed") ||
		!strings.Contains(v, "busy  not safe anymore: 1 uncommitted") {
		t.Errorf("summary does not say why the worktree stayed:\n%s", v)
	}
	press(a, tea.KeyEscape)
	if v := view(a); !strings.Contains(v, "busy") || !strings.Contains(v, "big") {
		t.Errorf("worktree that was not removed left the list:\n%s", v)
	}
}

// Quitting in the middle waits for the worktree being removed, which git
// could leave half deleted, and removes no more.
func TestQuitWhileRemovingStopsAfterCurrent(t *testing.T) {
	a, rm := removalApp(t)
	found(a, "one", 2000, true, lopper.Facts{})
	found(a, "two", 1000, true, lopper.Facts{})
	press(a, ' ')
	press(a, ' ')
	press(a, 'd')

	removing := press(a, tea.KeyEnter)
	if quit := press(a, 'q'); quit != nil {
		t.Fatalf("q during removal returned %T, want to wait for the current one", quit())
	}
	var quit bool
	for _, c := range removing().(tea.BatchMsg) {
		if msg, ok := c().(removedMsg); ok {
			_, next := a.Update(msg)
			_, quit = next().(tea.QuitMsg)
		}
	}
	if want := []string{"one"}; !quit || !slices.Equal(rm.calls, want) {
		t.Errorf("quit %v after removing %v, want to quit after %v", quit, rm.calls, want)
	}
}

// Scanning again starts from an empty list, and the scan it replaces,
// possibly still running, cannot add to it.
func TestScanAgainDropsOldScan(t *testing.T) {
	a, _ := removalApp(t)
	scans := 0
	a.scan = func(context.Context) <-chan engine.Event {
		scans++
		return make(chan engine.Event)
	}
	found(a, "gone", 1000, true, lopper.Facts{})
	found(a, "kept", 500, true, lopper.Facts{})
	press(a, 'd')
	settle(a, press(a, tea.KeyEnter))

	press(a, tea.KeyEnter)
	a.Update(eventMsg{ev: engine.WorktreeFound{Worktree: lopper.Worktree{ID: "late", Branch: "late"}}})
	v := view(a)
	if scans != 1 || strings.Contains(v, "kept") || strings.Contains(v, "late") {
		t.Errorf("scan again (%d scans) did not start over from an empty list:\n%s", scans, v)
	}
	a.Update(eventMsg{gen: a.gen, ev: engine.WorktreeFound{Worktree: lopper.Worktree{ID: "new", Branch: "new"}}})
	if v := view(a); !strings.Contains(v, "new") {
		t.Errorf("list does not show the new scan:\n%s", v)
	}
}
