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
		Facts: lopper.Facts{Dirty: &dirty, Unpushed: &unpushed},
		Verdict: lopper.Verdict{Level: lopper.LevelKeep, Reasons: []lopper.Reason{
			{Rule: "dirty", Level: lopper.LevelKeep, Message: "3 uncommitted change(s)"},
			{Rule: "unpushed", Level: lopper.LevelKeep, Message: "2 commit(s) not on any remote"},
			{Rule: "not-merged", Level: lopper.LevelReview, Message: "work not found in base branch"},
		}},
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

// A row reads "merged" only when the merged rule decided a safe verdict:
// a user will delete merged rows without looking closer.
func TestClassifyShowsMergedOnlyWhenSafe(t *testing.T) {
	merged := lopper.Reason{Rule: "merged", Level: lopper.LevelSafe}
	notMerged := lopper.Reason{Rule: "not-merged", Level: lopper.LevelReview}
	cases := []struct {
		name    string
		verdict lopper.Verdict
		want    state
		label   string
	}{
		{"merged", lopper.Verdict{Level: lopper.LevelSafe, Reasons: []lopper.Reason{merged}}, stateMerged, "merged"},
		{"folder gone", lopper.Verdict{Level: lopper.LevelSafe, Reasons: []lopper.Reason{
			{Rule: "prunable", Level: lopper.LevelSafe},
		}}, stateGone, "folder gone"},
		{"safe for a reason the list does not know", lopper.Verdict{Level: lopper.LevelSafe, Reasons: []lopper.Reason{
			{Rule: "future-rule", Level: lopper.LevelSafe},
		}}, stateUnknown, "future-rule"},
		{"merged but moved", lopper.Verdict{Level: lopper.LevelReview, Reasons: []lopper.Reason{
			{Rule: "moved", Level: lopper.LevelReview}, merged,
		}}, stateUnknown, "moved by hand"},
		{"not merged", lopper.Verdict{Level: lopper.LevelReview, Reasons: []lopper.Reason{notMerged}}, stateNotMerged, "not merged"},
		{"merged but unconfirmed", lopper.Verdict{Level: lopper.LevelReview, Reasons: []lopper.Reason{
			{Rule: "unconfirmed", Level: lopper.LevelReview}, merged,
		}}, stateUnknown, "not confirmed by git"},
		{"not merged and unchecked", lopper.Verdict{Level: lopper.LevelReview, Reasons: []lopper.Reason{
			{Rule: "incomplete", Level: lopper.LevelReview}, notMerged,
		}}, stateUnknown, "couldn't check"},
		{"no verdict yet", lopper.Verdict{}, stateChecking, "checking…"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, label := classify(&row{verdict: tc.verdict})
			if got != tc.want || label != tc.label {
				t.Errorf("classify = %v %q, want %v %q", got, label, tc.want, tc.label)
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

// A worktree whose directory is gone is safe to drop but was never
// checked for merging; the tally must not count it as merged.
func TestFolderGoneIsNotCountedAsMerged(t *testing.T) {
	a := testApp()
	a.Update(tea.WindowSizeMsg{Width: 100, Height: 20})
	a.Update(eventMsg{engine.WorktreeFound{Worktree: lopper.Worktree{ID: "g", Path: "/w/gone", Branch: "gone", Prunable: true}}})
	a.Update(eventMsg{engine.FactsUpdated{ID: "g", Verdict: lopper.Verdict{Level: lopper.LevelSafe, Reasons: []lopper.Reason{
		{Rule: "prunable", Level: lopper.LevelSafe, Message: "directory is already gone"},
	}}}})

	tally := plainLines(a)[3]
	if !strings.Contains(tally, "0 merged") || !strings.Contains(tally, "1 folder gone") {
		t.Errorf("tally counts a gone folder as merged: %q", tally)
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
		Verdict: lopper.Verdict{Level: lopper.LevelKeep, Reasons: []lopper.Reason{
			{Rule: "dirty", Level: lopper.LevelKeep}, {Rule: "unpushed", Level: lopper.LevelKeep},
		}},
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
