package tui

import (
	"context"
	"fmt"
	"path/filepath"
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
	calls []string // "id" or "id forced", or "measure id"
	fail  map[lopper.ID]error
	sizes map[lopper.ID]int64
}

func (r *remover) measure(_ context.Context, wt lopper.Worktree) *int64 {
	r.calls = append(r.calls, "measure "+string(wt.ID))
	if size, ok := r.sizes[wt.ID]; ok {
		return &size
	}
	return nil
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
	a.ctx, a.remove, a.measure = t.Context(), rm.remove, rm.measure
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
	if lines := plainLines(a); lineWith(t, lines, "work in these will be lost") > lineWith(t, lines, "wip") ||
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

// The confirmation follows the scan until the user confirms: sizes still
// being measured arrive, and a worktree that turns out to hold work
// moves under the warning. What is on screen at enter is what goes.
func TestConfirmationFollowsScanUntilConfirmed(t *testing.T) {
	a, rm := removalApp(t)
	a.Update(eventMsg{ev: engine.WorktreeFound{Worktree: lopper.Worktree{ID: "wt", Path: "/r/wt", Branch: "wt", Repo: lopper.Repo{Path: "/r"}}}})
	a.Update(eventMsg{ev: engine.FactsUpdated{ID: "wt", Facts: lopper.Facts{Dirty: new(0)}, Safe: true}})
	press(a, 'd')
	if v := view(a); strings.Contains(v, "work in these will be lost") {
		t.Fatalf("safe worktree is shown under the warning:\n%s", v)
	}

	a.Update(eventMsg{ev: engine.FactsUpdated{ID: "wt", Facts: lopper.Facts{Dirty: new(2), SizeBytes: new(int64(5_000_000))}}})
	lines := plainLines(a)
	if !strings.Contains(lines[lineWith(t, lines, "Remove")], "5.0 MB") ||
		lineWith(t, lines, "work in these will be lost") > lineWith(t, lines, "2 uncommitted") {
		t.Errorf("confirmation does not show the size and work that arrived:\n%s", strings.Join(lines, "\n"))
	}
	settle(a, press(a, tea.KeyEnter))
	if want := []string{"wt forced"}; !slices.Equal(rm.calls, want) {
		t.Errorf("removed %v, want %v: it was shown holding work when confirmed", rm.calls, want)
	}
}

// A worktree still being checked can be picked, but not confirmed until
// its facts show what would be lost; until then it is in a section of
// its own, neither safe nor holding work.
func TestConfirmationWaitsForChecks(t *testing.T) {
	a, rm := removalApp(t)
	a.Update(eventMsg{ev: engine.WorktreeFound{Worktree: lopper.Worktree{ID: "wt", Path: "/r/wt", Branch: "wt", Repo: lopper.Repo{Path: "/r"}}}})
	press(a, 'd')
	press(a, tea.KeyEnter)
	lines := plainLines(a)
	if len(rm.calls) > 0 || strings.Contains(strings.Join(lines, "\n"), "work in these will be lost") ||
		lineWith(t, lines, "still checking") > lineWith(t, lines, "wt  checking…") ||
		!strings.HasSuffix(strings.TrimSpace(lines[lineWith(t, lines, "Remove")]), "checking…") {
		t.Fatalf("worktree still being checked is not shown waiting apart (removed %v):\n%s",
			rm.calls, strings.Join(lines, "\n"))
	}

	a.Update(eventMsg{ev: engine.FactsUpdated{ID: "wt", Facts: lopper.Facts{SizeBytes: new(int64(1))}, Safe: true}})
	settle(a, press(a, tea.KeyEnter))
	if want := []string{"wt"}; !slices.Equal(rm.calls, want) {
		t.Errorf("removed %v once checked, want %v", rm.calls, want)
	}
}

// A worktree the scan had not measured yet is measured before it goes,
// so the summary still tells the space freed.
func TestRemovalMeasuresWhatScanHadNot(t *testing.T) {
	a, rm := removalApp(t)
	found(a, "known", 1_000_000, true, lopper.Facts{})
	a.Update(eventMsg{ev: engine.WorktreeFound{Worktree: lopper.Worktree{ID: "new", Path: "/r/new", Branch: "new", Repo: lopper.Repo{Path: "/r"}}}})
	a.Update(eventMsg{ev: engine.FactsUpdated{ID: "new", Safe: true}})
	rm.sizes = map[lopper.ID]int64{"new": 4_000_000}
	press(a, ' ')
	press(a, ' ')

	press(a, 'd')
	settle(a, press(a, tea.KeyEnter))
	if want := []string{"known", "measure new", "new"}; !slices.Equal(rm.calls, want) {
		t.Errorf("calls %v, want %v: only the unmeasured one, and before it is removed", rm.calls, want)
	}
	if v := view(a); !strings.Contains(v, "5.0 MB freed") {
		t.Errorf("summary does not count the size measured at removal:\n%s", v)
	}
}

// Worktrees of different repositories may share a branch name: the
// confirmation tells them apart by repository.
func TestConfirmationNamesRepositories(t *testing.T) {
	a, _ := removalApp(t)
	for _, repo := range []string{"app", "api"} {
		a.Update(eventMsg{ev: engine.WorktreeFound{Worktree: lopper.Worktree{
			ID: lopper.ID(repo), Path: "/code/" + repo + "-fix", Branch: "fix", Repo: lopper.Repo{Path: "/code/" + repo},
		}}})
		a.Update(eventMsg{ev: engine.FactsUpdated{ID: lopper.ID(repo), Safe: true}})
	}
	press(a, ' ')
	press(a, ' ')

	press(a, 'd')
	lines := plainLines(a)
	for _, want := range []string{"app  fix", "api  fix"} {
		if !strings.HasPrefix(strings.TrimSpace(lines[lineWith(t, lines, want)]), want) {
			t.Errorf("row does not start with its repository %q:\n%s", want, strings.Join(lines, "\n"))
		}
	}
}

// Every worktree to be removed can be seen before confirming, however
// many there are: the ones out of sight could hold work. Scrolled into
// them, the warning over them stays in sight, and what is out of sight
// is counted.
func TestConfirmationScrollsThroughLongSelection(t *testing.T) {
	a, _ := removalApp(t)
	a.Update(tea.WindowSizeMsg{Width: 100, Height: 12})
	for i := range 12 {
		found(a, fmt.Sprintf("wt%02d", i), int64(1000-i), i < 3, lopper.Facts{Dirty: new(i)})
	}
	for range 12 {
		press(a, ' ')
	}

	press(a, 'd')
	if v := view(a); !strings.Contains(v, "wt00") || strings.Contains(v, "wt11") || !strings.Contains(v, "↓ 8 more") ||
		!strings.Contains(v, "scroll") {
		t.Fatalf("long selection does not start at the top, counting the rest, with a way to scroll:\n%s", v)
	}
	for range 20 {
		press(a, tea.KeyDown)
	}
	v := view(a)
	if !strings.Contains(v, "wt11") || strings.Contains(v, "wt00") || !strings.Contains(v, "↑ 7 more") {
		t.Errorf("scrolling down does not reach the last worktree, counting the ones above:\n%s", v)
	}
	if !strings.Contains(v, "work in these will be lost") {
		t.Errorf("warning scrolled out of sight over the worktrees it is about:\n%s", v)
	}
}

// The summary says why each worktree was not removed, in a column the
// screen holds however long the reason: a line wider than the screen
// broke the whole summary, and a long path pushed the cause out of sight.
func TestSummaryTellsWhyInColumn(t *testing.T) {
	a, rm := removalApp(t)
	a.Update(tea.WindowSizeMsg{Width: 72, Height: 20})
	found(a, "unpushed", 100, false, lopper.Facts{})
	found(a, "feature/login-page", 90, true, lopper.Facts{})
	rm.fail = map[lopper.ID]error{
		"unpushed":           fmt.Errorf("failed to delete '%s': Permission denied", filepath.Join(t.TempDir(), "code", "app", ".claude", "worktrees", "unpushed")),
		"feature/login-page": &engine.NotSafeError{Facts: lopper.Facts{Dirty: new(1), Unpushed: new(0), Merged: new(lopper.MergedFF)}},
	}
	press(a, ' ')
	press(a, ' ')
	press(a, 'd')
	settle(a, press(a, tea.KeyEnter))

	lines := plainLines(a)
	gitErr, notSafe := lines[lineWith(t, lines, "failed to delete")], lines[lineWith(t, lines, "not safe anymore")]
	if !strings.Contains(gitErr, "unpushed            failed to delete 'unpushed': Permission denied") {
		t.Errorf("reason does not name the folder shortly and keep the cause: %q", gitErr)
	}
	if strings.Index(gitErr, "failed") != strings.Index(notSafe, "not safe") {
		t.Errorf("reasons are not in one column:\n%s\n%s", gitErr, notSafe)
	}

	// Wider than the screen, the summary is no longer centered, and every
	// line of it runs past the edge, cut off: "      …".
	a.Update(tea.WindowSizeMsg{Width: 50, Height: 20})
	for _, l := range plainLines(a) {
		if strings.TrimSpace(l) == "…" {
			t.Errorf("summary runs past the edge of a 50-cell screen:\n%s", view(a))
			break
		}
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
