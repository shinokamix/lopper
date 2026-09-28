package tui

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

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
	a.Update(eventMsg{engine.WorktreeFound{Worktree: lopper.Worktree{
		ID: "wt", Path: "/home/me/code/app/.claude/worktrees/fix-login-redirect", Branch: "fix/login", Repo: repo,
	}}})
	a.Update(eventMsg{engine.WorktreeFound{Worktree: lopper.Worktree{
		ID: "deps", Path: "/home/me/code/api-deps", Branch: "chore/deps", Repo: lopper.Repo{Path: "/home/me/code/api"},
	}}})
	dirty, unpushed := 3, 2
	a.Update(eventMsg{engine.FactsUpdated{
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
	a.Update(eventMsg{engine.WorktreeFound{Worktree: lopper.Worktree{
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

func TestCursorStaysOnWorktreeWhenOneSortsAbove(t *testing.T) {
	a := testApp()
	a.Update(tea.WindowSizeMsg{Width: 100, Height: 20})
	a.Update(eventMsg{engine.WorktreeFound{Worktree: lopper.Worktree{ID: "m", Path: "/w/middle", Branch: "middle"}}})
	a.Update(eventMsg{engine.WorktreeFound{Worktree: lopper.Worktree{ID: "z", Path: "/w/zulu", Branch: "zulu"}}})
	a.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	a.Update(eventMsg{engine.WorktreeFound{Worktree: lopper.Worktree{ID: "a", Path: "/w/alpha", Branch: "alpha"}}})

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
	a.Update(eventMsg{engine.WorktreeFound{Worktree: lopper.Worktree{ID: "a", Path: "/w/alpha", Branch: "alpha"}}})
	a.Update(eventMsg{engine.FactsUpdated{ID: "a", Facts: lopper.Facts{SizeBytes: &size}}})
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

// A size still being measured shows a one-cell placeholder; the status
// column must stay where it is on rows with a known size.
func TestStatusColumnAlignsWhileSizeIsUnknown(t *testing.T) {
	a := testApp()
	a.Update(tea.WindowSizeMsg{Width: 60, Height: 20})
	clean := 0
	merged := lopper.Facts{Dirty: &clean, Unpushed: &clean, Merged: new(lopper.MergedFF)}
	measured := merged
	measured.SizeBytes = new(int64(1000))
	for _, id := range []string{"measured", "measuring"} {
		a.Update(eventMsg{engine.WorktreeFound{Worktree: lopper.Worktree{ID: lopper.ID(id), Branch: "a-branch-long-enough-to-be-cut-" + id}}})
	}
	a.Update(eventMsg{engine.FactsUpdated{ID: "measured", Facts: measured, Safe: true}})
	a.Update(eventMsg{engine.FactsUpdated{ID: "measuring", Facts: merged, Safe: true}})

	lines := plainLines(a)
	at := lineWith(t, lines, "a-branch") // measured first: it is larger
	known, unknown := lines[at], strings.TrimRight(lines[at+1], " ")
	if !strings.Contains(known, "1.0 kB") || !strings.HasSuffix(unknown, "…") {
		t.Fatalf("want the measured row, then the one still being measured:\n%s\n%s", known, unknown)
	}
	if strings.Index(known, "merged") != strings.Index(unknown, "merged") {
		t.Errorf("status column moves on a row with unknown size:\n%s\n%s", known, unknown)
	}
}

func TestSelectionAndCursorBandsDiffer(t *testing.T) {
	a := testApp()
	a.Update(tea.WindowSizeMsg{Width: 100, Height: 20})
	a.Update(eventMsg{engine.WorktreeFound{Worktree: lopper.Worktree{ID: "a", Branch: "alpha"}}})
	a.Update(eventMsg{engine.WorktreeFound{Worktree: lopper.Worktree{ID: "b", Branch: "bravo"}}})
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
		a.Update(eventMsg{engine.WorktreeFound{Worktree: lopper.Worktree{
			ID: lopper.ID(id), Path: "/w/" + id, Branch: branch, Repo: lopper.Repo{Path: repo},
		}}})
		a.Update(eventMsg{engine.FactsUpdated{ID: lopper.ID(id), Facts: lopper.Facts{SizeBytes: &size}}})
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
	a.Update(eventMsg{engine.WorktreeFound{Worktree: lopper.Worktree{
		ID: "wt", Path: repo.Path + "/.claude/worktrees/x", Branch: "feature/a-branch-name-longer-than-the-screen", Repo: repo,
	}}})
	dirty, unpushed, size := 3, 2, int64(1_200_000_000)
	a.Update(eventMsg{engine.FactsUpdated{
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
	if row := lines[lineWith(t, lines, "feature/")]; !strings.Contains(row, "1.2 GB") || !strings.Contains(row, "3 uncom") {
		t.Errorf("narrow row lost its status or size: %q", row)
	}
}
