package tui

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/shinokamix/lopper/internal/engine"
	"github.com/shinokamix/lopper/internal/lopper"
)

func testApp() *app {
	return &app{store: newStore(), keys: defaultKeys(), theme: newTheme(true), help: help.New(), spin: spinner.New(), list: newList([]alias{{"/home/me", "~"}})}
}

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

func TestNavigationKeepsSelectionVisible(t *testing.T) {
	a := testApp()
	a.Update(tea.WindowSizeMsg{Width: 100, Height: 8})
	for _, branch := range []string{"first", "second", "third"} {
		a.Update(eventMsg{ev: engine.WorktreeFound{Worktree: lopper.Worktree{ID: lopper.ID(branch), Branch: branch}}})
	}
	a.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	a.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	a.Update(tea.KeyPressMsg{Code: ' '})
	view := a.View().Content
	if !strings.Contains(view, "third") || !strings.Contains(view, "1 selected") || strings.Contains(view, "first") {
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

func TestRowShowsFactsAndPathUnderRepository(t *testing.T) {
	a := testApp()
	repo := lopper.Repo{Path: "/home/me/code/app", DefaultBranch: "main"}
	a.Update(eventMsg{ev: engine.WorktreeFound{Worktree: lopper.Worktree{
		ID: "wt", Path: "/home/me/code/app/.claude/worktrees/fix-login-redirect", Branch: "fix/login", Repo: repo,
	}}})
	a.Update(eventMsg{ev: engine.WorktreeFound{Worktree: lopper.Worktree{
		ID: "deps", Path: "/home/me/code/api-deps", Branch: "chore/deps", Repo: lopper.Repo{Path: "/home/me/code/api"},
	}}})
	dirty, unpushed := 3, 2
	a.Update(eventMsg{ev: engine.FactsUpdated{
		ID:    "wt",
		Facts: lopper.Facts{Dirty: &dirty, Unpushed: &unpushed, Merged: new(lopper.NotMerged)},
	}})

	a.Update(tea.WindowSizeMsg{Width: 100, Height: 20})
	a.Update(tea.KeyPressMsg{Code: tea.KeyDown}) // from chore/deps in api to fix/login in app
	lines := plainLines(a)
	i := lineWith(t, lines, "fix/login")
	if !strings.HasPrefix(strings.TrimSpace(lines[i-1]), filepath.FromSlash("app  ~/code ")) {
		t.Errorf("row is not under its repository header: %q", lines[i-1])
	}
	j := lineWith(t, lines, "chore/deps")
	if !strings.HasPrefix(strings.TrimSpace(lines[j-1]), filepath.FromSlash("api  ~/code ")) {
		t.Errorf("row of another repository is not under its own header: %q", lines[j-1])
	}
	if !strings.Contains(lines[i], "3 uncommitted · 2 unpushed") {
		t.Errorf("row under the cursor does not show its facts: %q", lines[i])
	}
	if got := statusLine(lines); got != filepath.FromSlash("~/code/app/.claude/worktrees/fix-login-redirect") {
		t.Errorf("status line does not show the path of the row under the cursor: %q", got)
	}

	a.Update(tea.WindowSizeMsg{Width: 30, Height: 20})
	lines = plainLines(a)
	if path := statusLine(lines); path != filepath.FromSlash("~/…/fix-login-redirect") {
		t.Errorf("narrow path does not keep whole trailing directories: %q", path)
	}
}

// Agents put worktrees deep in temporary directories; such paths must
// neither push a line past the screen nor hide where the worktree is.
func TestLongPathsFitTheScreen(t *testing.T) {
	a := testApp()
	a.list = newList([]alias{{"/private/tmp", "tmp"}, {"/home/me", "~"}})
	scratch := "/private/tmp/claude-501/-Users-me-Documents-projects-workspace-cleaner--claude-worktrees-discovery/scratchpad"
	a.Update(eventMsg{ev: engine.WorktreeFound{Worktree: lopper.Worktree{
		ID: "c3", Path: scratch + "/lab/outside/c3", Branch: "c3-dotbare",
		Repo: lopper.Repo{Path: scratch + "/lab/proj/.bare"},
	}}})
	a.Update(tea.WindowSizeMsg{Width: 80, Height: 20})

	lines := plainLines(a)
	for _, l := range lines {
		if w := ansi.StringWidth(l); w > 80 {
			t.Errorf("line is %d cells wide on an 80-cell screen: %q", w, l)
		}
	}
	i := lineWith(t, lines, "c3-dotbare")
	if !strings.HasPrefix(strings.TrimSpace(lines[i-1]), filepath.FromSlash("proj  tmp/…/")) {
		t.Errorf("bare repository header is not named after its project with a short path: %q", lines[i-1])
	}
	if path := statusLine(lines); path != filepath.FromSlash("tmp/…/scratchpad/lab/outside/c3") {
		t.Errorf("worktree path is not shortened by directories: %q", path)
	}
}

// A row shows "merged" in its color only when the worktree is safe to
// delete: users delete merged rows without looking closer.
func TestFactsColorMergedOnlyWhenSafe(t *testing.T) {
	clean, dirty := 0, 1
	merged, notMerged := new(lopper.MergedFF), new(lopper.NotMerged)
	cases := []struct {
		name   string
		row    row
		want   string
		marked bool // "merged" is in its color
	}{
		{"merged", row{checked: true, safe: true, facts: lopper.Facts{Dirty: &clean, Unpushed: &clean, Merged: merged}}, "merged", true},
		{"merged but dirty", row{checked: true, facts: lopper.Facts{Dirty: &dirty, Unpushed: &clean, Merged: merged}}, "1 uncommitted · merged", false},
		{"merged but moved", row{checked: true, worktree: lopper.Worktree{MovedFrom: "/old"}, facts: lopper.Facts{Dirty: &clean, Unpushed: &clean, Merged: merged}}, "moved by hand · merged", false},
		{"not merged and unchecked", row{checked: true, facts: lopper.Facts{Unpushed: &clean, Merged: notMerged}}, "couldn't check · not merged", false},
		{"folder gone", row{checked: true, safe: true, worktree: lopper.Worktree{Prunable: true}}, "folder gone", false},
		{"no facts yet", row{}, "checking…", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var texts []string
			marked := false
			for _, n := range facts(&tc.row) {
				texts = append(texts, n.Text)
				marked = marked || n.Kind == lopper.NoteMerged
			}
			if got := strings.Join(texts, " · "); got != tc.want || marked != tc.marked {
				t.Errorf("facts = %q (merged in color: %v), want %q (%v)", got, marked, tc.want, tc.marked)
			}
		})
	}
}

// A row shows all its facts when the screen has room, and otherwise the
// most pressing ones and how many more there are: a fact that makes a
// worktree unsafe must never vanish without a trace.
func TestRowShowsAllFactsOrCountsTheRest(t *testing.T) {
	a := testApp()
	a.Update(eventMsg{ev: engine.WorktreeFound{Worktree: lopper.Worktree{
		ID: "wt", Path: "/w/wt", Branch: "fix/login", Unconfirmed: "HEAD is missing",
	}}})
	dirty, clean := 3, 0
	a.Update(eventMsg{ev: engine.FactsUpdated{
		ID: "wt", Facts: lopper.Facts{Dirty: &dirty, Unpushed: &clean, Merged: new(lopper.MergedFF)},
	}})

	a.Update(tea.WindowSizeMsg{Width: 120, Height: 20})
	if row := plainLines(a)[lineWith(t, plainLines(a), "fix/login")]; !strings.Contains(row, "3 uncommitted · not confirmed by git · merged") {
		t.Errorf("wide row does not show every fact: %q", row)
	}
	a.Update(tea.WindowSizeMsg{Width: 50, Height: 20})
	if row := plainLines(a)[lineWith(t, plainLines(a), "fix/login")]; !strings.Contains(row, "3 uncommitted +2") {
		t.Errorf("narrow row does not lead with the work at stake and count the rest: %q", row)
	}
}

func TestCursorStaysOnWorktreeWhenOneSortsAbove(t *testing.T) {
	a := testApp()
	a.Update(tea.WindowSizeMsg{Width: 100, Height: 20})
	a.Update(eventMsg{ev: engine.WorktreeFound{Worktree: lopper.Worktree{ID: "m", Path: "/w/middle", Branch: "middle"}}})
	a.Update(eventMsg{ev: engine.WorktreeFound{Worktree: lopper.Worktree{ID: "z", Path: "/w/zulu", Branch: "zulu"}}})
	a.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	a.Update(eventMsg{ev: engine.WorktreeFound{Worktree: lopper.Worktree{ID: "a", Path: "/w/alpha", Branch: "alpha"}}})

	if got := statusLine(plainLines(a)); got != "/w/zulu" { // matches no alias, so shown exactly as given
		t.Errorf("cursor left zulu when alpha sorted above it: status line shows %q", got)
	}
}

// statusLine is the line above the key help: the path under the cursor.
func statusLine(lines []string) string {
	return strings.TrimSpace(lines[len(lines)-2])
}

// The selection total sits at the right end of the status line, under
// the size column, not among the key help.
func TestSelectionIsShownBesideThePath(t *testing.T) {
	a := testApp()
	a.Update(tea.WindowSizeMsg{Width: 100, Height: 20})
	size := int64(2_000_000)
	a.Update(eventMsg{ev: engine.WorktreeFound{Worktree: lopper.Worktree{ID: "a", Path: "/w/alpha", Branch: "alpha"}}})
	a.Update(eventMsg{ev: engine.FactsUpdated{ID: "a", Facts: lopper.Facts{SizeBytes: &size}}})
	a.Update(tea.KeyPressMsg{Code: ' '})

	lines := plainLines(a)
	status := statusLine(lines)
	if !strings.HasPrefix(status, "/w/alpha") || !strings.HasSuffix(status, "1 selected · 2.0 MB") {
		t.Errorf("status line does not show the path and then the selection: %q", status)
	}
	if keys := lines[len(lines)-1]; strings.Contains(keys, "selected") {
		t.Errorf("selection is shown among the key help: %q", keys)
	}
}

// A size still being measured shows a one-cell spinner; the status
// column must stay where it is on rows with a known size.
func TestStatusColumnAlignsWhileSizeIsUnknown(t *testing.T) {
	a := testApp()
	a.Update(tea.WindowSizeMsg{Width: 60, Height: 20})
	clean := 0
	merged := lopper.Facts{Dirty: &clean, Unpushed: &clean, Merged: new(lopper.MergedFF)}
	measured := merged
	measured.SizeBytes = new(int64(1000))
	for _, id := range []string{"measured", "measuring"} {
		a.Update(eventMsg{ev: engine.WorktreeFound{Worktree: lopper.Worktree{ID: lopper.ID(id), Branch: "a-branch-long-enough-to-be-cut-" + id}}})
	}
	a.Update(eventMsg{ev: engine.FactsUpdated{ID: "measured", Facts: measured, Safe: true}})
	a.Update(eventMsg{ev: engine.FactsUpdated{ID: "measuring", Facts: merged, Safe: true}})

	lines := plainLines(a)
	at := lineWith(t, lines, "a-branch") // measured first: it is larger
	known, unknown := lines[at], strings.TrimRight(lines[at+1], " ")
	if !strings.Contains(known, "1.0 kB") || !strings.HasSuffix(unknown, spinning(a)) {
		t.Fatalf("want the measured row, then the one still being measured:\n%s\n%s", known, unknown)
	}
	if strings.Index(known, "merged") != strings.Index(unknown, "merged") {
		t.Errorf("status column moves on a row with unknown size:\n%s\n%s", known, unknown)
	}
}

// spinning is the spinner's frame now: what a size being measured shows.
func spinning(a *app) string { return strings.TrimSpace(a.spin.View()) }

// A size that could not be measured must not look as if it still were:
// the spinner would turn forever.
func TestSizeSpinsOnlyWhileBeingMeasured(t *testing.T) {
	a := testApp()
	a.Update(tea.WindowSizeMsg{Width: 60, Height: 20})
	for _, id := range []string{"measuring", "unmeasurable"} {
		a.Update(eventMsg{ev: engine.WorktreeFound{Worktree: lopper.Worktree{ID: lopper.ID(id), Branch: id}}})
	}
	a.Update(eventMsg{ev: engine.FactsUpdated{ID: "measuring"}})
	a.Update(eventMsg{ev: engine.FactsUpdated{ID: "unmeasurable", Final: true}})

	lines := plainLines(a)
	if row := strings.TrimRight(lines[lineWith(t, lines, "measuring")], " "); !strings.HasSuffix(row, spinning(a)) {
		t.Errorf("size being measured does not show the spinner: %q", row)
	}
	if row := strings.TrimRight(lines[lineWith(t, lines, "unmeasurable")], " "); !strings.HasSuffix(row, "?") {
		t.Errorf("size that could not be measured does not show as unknown: %q", row)
	}
}

func TestSelectionAndCursorBandsDiffer(t *testing.T) {
	a := testApp()
	a.Update(tea.WindowSizeMsg{Width: 100, Height: 20})
	a.Update(eventMsg{ev: engine.WorktreeFound{Worktree: lopper.Worktree{ID: "a", Branch: "alpha"}}})
	a.Update(eventMsg{ev: engine.WorktreeFound{Worktree: lopper.Worktree{ID: "b", Branch: "bravo"}}})
	a.Update(tea.KeyPressMsg{Code: ' '}) // selects alpha and moves on to bravo

	if !onBand(a, "alpha", a.theme.picked) || onBand(a, "alpha", a.theme.cursor) {
		t.Errorf("row selected with space does not show the selection band: %q", rawLine(a, "alpha"))
	}
	if !onBand(a, "bravo", a.theme.cursor) || onBand(a, "bravo", a.theme.picked) {
		t.Errorf("cursor did not move on to the next row: %q", rawLine(a, "bravo"))
	}

	a.Update(tea.KeyPressMsg{Code: tea.KeyUp})
	if !onBand(a, "alpha", a.theme.pickedCursor) {
		t.Errorf("selected row under the cursor looks like an unselected one: %q", rawLine(a, "alpha"))
	}
}

// onBand reports whether the row showing text is drawn on style's background.
func onBand(a *app, text string, style lipgloss.Style) bool {
	bg := style.Render(" ")
	return strings.Contains(rawLine(a, text), bg[:strings.Index(bg, "m")+1])
}

// rawLine returns the styled line of the view containing text, or "".
func rawLine(a *app, text string) string {
	for l := range strings.SplitSeq(a.View().Content, "\n") {
		if strings.Contains(ansi.Strip(l), text) {
			return l
		}
	}
	return ""
}

// updatingApp is the app as Run starts it with release v0.2.0 to offer.
// scans counts the scans started, skipped the releases skipped.
func updatingApp(t *testing.T, install func(context.Context, string) error) (a *app, scans *int, skipped *[]string) {
	a = testApp()
	a.ctx = t.Context()
	scans, skipped = new(int), new([]string)
	a.scan = func(context.Context) <-chan engine.Event {
		*scans++
		ch := make(chan engine.Event)
		close(ch)
		return ch
	}
	a.updates = Updates{
		Current: "v0.1.0", Latest: "v0.2.0", Repo: "https://github.com/shinokamix/lopper",
		Install: install,
		Skip:    func(tag string) { *skipped = append(*skipped, tag) },
	}
	a.offer = &offer{tag: "v0.2.0"}
	a.Update(tea.WindowSizeMsg{Width: 120, Height: 20})
	return a, scans, skipped
}

// A newer release is offered on its own screen before anything is
// scanned; skipping it scans with the running version and is remembered.
func TestNewerReleaseIsOfferedBeforeTheScan(t *testing.T) {
	a, scans, skipped := updatingApp(t, nil)
	screen := view(a)
	for _, want := range []string{"lopper v0.1.0 → v0.2.0", "https://github.com/shinokamix/lopper/releases/tag/v0.2.0", "enter update", "esc skip this version"} {
		if !strings.Contains(screen, want) {
			t.Errorf("update screen lacks %q:\n%s", want, screen)
		}
	}
	if *scans != 0 {
		t.Errorf("scan started while the update is offered")
	}

	settle(a, press(a, tea.KeyEscape))
	if screen := view(a); *scans != 1 || strings.Contains(screen, "v0.2.0") {
		t.Errorf("skipping did not go on to scan (%d scans):\n%s", *scans, screen)
	}
	if !slices.Equal(*skipped, []string{"v0.2.0"}) {
		t.Errorf("skipped %q, want v0.2.0", *skipped)
	}
}

// Enter installs the offered release; a failure says why and enter tries
// again; once installed, enter restarts into it rather than scanning.
func TestOfferedReleaseIsInstalledWithEnter(t *testing.T) {
	var installed []string
	a, scans, _ := updatingApp(t, func(_ context.Context, tag string) error {
		installed = append(installed, tag)
		if len(installed) == 1 {
			return errors.New("download: connection reset")
		}
		return nil
	})

	settle(a, press(a, tea.KeyEnter))
	if screen := view(a); !strings.Contains(screen, "update failed: download: connection reset") || !strings.Contains(screen, "enter update") {
		t.Errorf("failed install is not shown with a way to retry:\n%s", screen)
	}
	settle(a, press(a, tea.KeyEnter))
	if len(installed) != 2 || installed[1] != "v0.2.0" {
		t.Fatalf("installed %q, want v0.2.0 again after the failure", installed)
	}
	if screen := view(a); !strings.Contains(screen, "Updated to lopper v0.2.0") || !strings.Contains(screen, "enter restart") || strings.Contains(screen, "failed") {
		t.Errorf("finished install is not shown:\n%s", screen)
	}
	if _, isQuit := press(a, tea.KeyEnter)().(tea.QuitMsg); !isQuit || !a.restart {
		t.Errorf("enter after the update did not quit to restart")
	}
	if *scans != 0 {
		t.Errorf("scan started though lopper restarts")
	}
}

// Where lopper may not write, installing again would fail again: only
// skipping is offered, with the error saying how to update instead.
func TestInstallWithoutPermissionIsNotRetried(t *testing.T) {
	var tries int
	a, _, _ := updatingApp(t, func(context.Context, string) error {
		tries++
		return fmt.Errorf("cannot write to /usr/local/bin: %w; run sudo lopper update", fs.ErrPermission)
	})
	settle(a, press(a, tea.KeyEnter))
	settle(a, press(a, tea.KeyEnter))
	screen := view(a)
	if tries != 1 || strings.Contains(screen, "enter update") || !strings.Contains(screen, "esc skip") {
		t.Errorf("install without permission is offered again (%d tries):\n%s", tries, screen)
	}
	if !strings.Contains(screen, "run sudo lopper update") {
		t.Errorf("the way to update is not shown:\n%s", screen)
	}
}

// While the release installs, q quits at once and stops the download,
// which could otherwise hold lopper open for minutes.
func TestQuitWhileInstallingStopsIt(t *testing.T) {
	a, _, _ := updatingApp(t, func(ctx context.Context, _ string) error {
		<-ctx.Done() // a download that never ends
		return ctx.Err()
	})
	install := press(a, tea.KeyEnter)
	if screen := view(a); !strings.Contains(screen, "installing…") || !strings.Contains(screen, "q quit") {
		t.Errorf("installing screen does not offer q:\n%s", screen)
	}
	if _, isQuit := press(a, 'q')().(tea.QuitMsg); !isQuit || a.restart {
		t.Error("q did not quit while installing")
	}
	done := make(chan struct{})
	go func() { settle(a, install); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the install went on after q")
	}
}

// On a small screen, every way out of the update screen stays visible,
// and so does what to do about an install that failed.
func TestUpdateScreenFitsSmallScreen(t *testing.T) {
	a, _, _ := updatingApp(t, func(context.Context, string) error {
		return fmt.Errorf("cannot write to /usr/local/bin: open /usr/local/bin/.lopper-update-3141592: %w; run sudo lopper update", fs.ErrPermission)
	})
	a.Update(tea.WindowSizeMsg{Width: 34, Height: 8})
	lines := plainLines(a)
	for _, want := range []string{"enter update", "esc skip this version", "q quit"} {
		if !strings.Contains(view(a), want) {
			t.Errorf("small update screen lacks %q:\n%s", want, view(a))
		}
	}
	for _, l := range lines {
		if w := ansi.StringWidth(l); w > 34 {
			t.Errorf("line is %d cells wide on a 34-cell screen: %q", w, l)
		}
	}
	if len(lines) > 8 {
		t.Errorf("%d lines on an 8-line screen:\n%s", len(lines), view(a))
	}

	settle(a, press(a, tea.KeyEnter))
	text := strings.Join(strings.Fields(view(a)), " ")
	for _, want := range []string{"update failed", "run sudo lopper update", "esc skip this version", "q quit"} {
		if !strings.Contains(text, want) {
			t.Errorf("after the error, small update screen lacks %q:\n%s", want, view(a))
		}
	}
	if n := len(plainLines(a)); n > 8 {
		t.Errorf("%d lines on an 8-line screen:\n%s", n, view(a))
	}
	a.Update(tea.WindowSizeMsg{Width: 34, Height: 6})
	if screen := view(a); !strings.Contains(screen, "esc skip this version") || !strings.Contains(screen, "q quit") {
		t.Errorf("after the error, a small screen lost the way out:\n%s", screen)
	}
	if n := len(plainLines(a)); n > 6 {
		t.Errorf("%d lines on a 6-line screen:\n%s", n, view(a))
	}
}

// Quitting may drop the command that would install before it runs: then
// no install starts, and lopper exits rather than wait for it.
func TestInstallDroppedByQuitNeitherRunsNorHoldsExit(t *testing.T) {
	var calls int
	a, _, _ := updatingApp(t, func(context.Context, string) error { calls++; return nil })
	cmd := a.install()
	exited := make(chan struct{})
	go func() { a.installs.close(); close(exited) }()
	select {
	case <-exited:
	case <-time.After(5 * time.Second):
		t.Fatal("exit waits for an install that never started")
	}
	if msg := cmd(); msg != nil || calls != 0 {
		t.Errorf("install ran after the TUI quit: %v, %d calls", msg, calls)
	}
}

// An install that started is waited for on exit: on Windows, exiting
// midway could leave no lopper binary.
func TestExitWaitsForRunningInstall(t *testing.T) {
	started, finish := make(chan struct{}), make(chan struct{})
	a, _, _ := updatingApp(t, func(context.Context, string) error {
		close(started)
		<-finish
		return nil
	})
	go a.install()()
	<-started
	exited := make(chan struct{})
	go func() { a.installs.close(); close(exited) }()
	select {
	case <-exited:
		t.Fatal("exit did not wait for the running install")
	case <-time.After(50 * time.Millisecond):
	}
	close(finish)
	<-exited
}

func plainLines(a *app) []string {
	return strings.Split(ansi.Strip(a.View().Content), "\n")
}

// lineWith returns the index of the first line containing s.
func lineWith(t *testing.T, lines []string, s string) int {
	t.Helper()
	for i, l := range lines {
		if strings.Contains(l, s) {
			return i
		}
	}
	t.Fatalf("no line with %q in:\n%s", s, strings.Join(lines, "\n"))
	return 0
}

func TestLargestWorktreesComeFirst(t *testing.T) {
	a := testApp()
	a.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	found := func(id, branch, repo string, size int64) {
		a.Update(eventMsg{ev: engine.WorktreeFound{Worktree: lopper.Worktree{
			ID: lopper.ID(id), Path: "/w/" + id, Branch: branch, Repo: lopper.Repo{Path: repo},
		}}})
		a.Update(eventMsg{ev: engine.FactsUpdated{ID: lopper.ID(id), Facts: lopper.Facts{SizeBytes: &size}}})
	}
	// By name, alpha and a-one would come first; by size they come last.
	found("a1", "a-one", "/r/alpha", 100)
	found("a2", "a-two", "/r/alpha", 300)
	found("z1", "z-huge", "/r/zeta", 1000)

	lines := plainLines(a)
	zeta, two, one := lineWith(t, lines, "zeta"), lineWith(t, lines, "a-two"), lineWith(t, lines, "a-one")
	if zeta >= two || two >= one {
		t.Errorf("want repo zeta (1 kB) above alpha (400 B), and a-two (300 B) above a-one (100 B):\n%s", strings.Join(lines, "\n"))
	}
}

// Scrolling assumes one screen line per line of the view, so on a narrow
// terminal every line must still fit, and a row must keep its size.
func TestNarrowScreenKeepsEveryLineWithinWidth(t *testing.T) {
	a := testApp()
	a.Update(tea.WindowSizeMsg{Width: 40, Height: 20})
	repo := lopper.Repo{Path: "/home/me/code/a-repository-with-a-long-name"}
	a.Update(eventMsg{ev: engine.WorktreeFound{Worktree: lopper.Worktree{
		ID: "wt", Path: repo.Path + "/.claude/worktrees/x", Branch: "feature/a-branch-name-longer-than-the-screen", Repo: repo,
	}}})
	dirty, unpushed, size := 3, 2, int64(1_200_000_000)
	a.Update(eventMsg{ev: engine.FactsUpdated{
		ID:    "wt",
		Facts: lopper.Facts{Dirty: &dirty, Unpushed: &unpushed, SizeBytes: &size},
	}})
	a.Update(tea.KeyPressMsg{Code: ' '})

	lines := plainLines(a)
	for _, l := range lines {
		if w := ansi.StringWidth(l); w > 40 {
			t.Errorf("line is %d cells wide on a 40-cell screen: %q", w, l)
		}
	}
	if row := lines[lineWith(t, lines, "feature/")]; !strings.Contains(row, "1.2 GB") || !strings.Contains(row, "3 un… +2") {
		t.Errorf("narrow row lost its facts or size: %q", row)
	}
}
