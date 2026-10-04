package tui

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/shinokamix/lopper/internal/engine"
	"github.com/shinokamix/lopper/internal/lopper"
)

// sized reports worktree id of repository repo, measured at size.
func sized(a *app, id, repo string, size int64) {
	a.Update(eventMsg{ev: engine.WorktreeFound{Worktree: lopper.Worktree{
		ID: lopper.ID(id), Path: "/w/" + id, Branch: id, Repo: lopper.Repo{Path: repo},
	}}})
	a.Update(eventMsg{ev: engine.FactsUpdated{ID: lopper.ID(id), Facts: lopper.Facts{SizeBytes: &size}}})
}

// typeText types s as a terminal sends it, one key per character.
func typeText(a *app, s string) {
	for _, c := range s {
		a.Update(tea.KeyPressMsg{Code: c, Text: string(c)})
	}
}

// order returns the lines of the view that contain the texts, in the
// order they are shown.
func order(t *testing.T, a *app, texts ...string) []string {
	t.Helper()
	lines := plainLines(a)
	slices.SortStableFunc(texts, func(x, y string) int { return lineWith(t, lines, x) - lineWith(t, lines, y) })
	return texts
}

// Rows stay where they were found while the scan fills them in. A size
// arriving does not move a row, and a longer branch found later does not
// push the facts of the others aside.
func TestRowsHoldStillWhileTheScanGoesOn(t *testing.T) {
	a := testApp()
	a.Update(tea.WindowSizeMsg{Width: 100, Height: 20})
	a.Update(eventMsg{ev: engine.WorktreeFound{Worktree: lopper.Worktree{ID: "small", Branch: "small", Repo: lopper.Repo{Path: "/r/one"}}}})
	a.Update(eventMsg{ev: engine.WorktreeFound{Worktree: lopper.Worktree{ID: "large", Branch: "large", Repo: lopper.Repo{Path: "/r/two"}}}})
	lines := plainLines(a)
	at := lineWith(t, lines, "small")
	factsAt := strings.Index(lines[at], "checking…")

	size := int64(2_000_000_000)
	a.Update(eventMsg{ev: engine.FactsUpdated{ID: "large", Facts: lopper.Facts{SizeBytes: &size}}})
	a.Update(eventMsg{ev: engine.WorktreeFound{Worktree: lopper.Worktree{ID: "long", Branch: "feature/a-branch-longer-than-any-before", Repo: lopper.Repo{Path: "/r/two"}}}})

	lines = plainLines(a)
	if now := lineWith(t, lines, "small"); now != at {
		t.Errorf("row moved from line %d to %d as the scan went on:\n%s", at, now, strings.Join(lines, "\n"))
	}
	if now := strings.Index(lines[at], "checking…"); now != factsAt {
		t.Errorf("facts moved from column %d to %d when a longer branch was found:\n%s", factsAt, now, strings.Join(lines, "\n"))
	}
}

// s shows the largest worktrees first, groups by their total and rows
// by their own, with the rows still being measured after the others; s
// again returns to the order found.
func TestSShowsLargestFirst(t *testing.T) {
	a := testApp()
	a.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	sized(a, "a-one", "/r/alpha", 100)
	a.Update(eventMsg{ev: engine.WorktreeFound{Worktree: lopper.Worktree{ID: "a-new", Branch: "a-new", Repo: lopper.Repo{Path: "/r/alpha"}}}})
	sized(a, "a-two", "/r/alpha", 300)
	sized(a, "z-huge", "/r/zeta", 1000)

	press(a, 's')
	if got, want := order(t, a, "a-one", "a-new", "a-two", "zeta"), []string{"zeta", "a-two", "a-one", "a-new"}; !slices.Equal(got, want) {
		t.Errorf("largest first shows %q, want %q:\n%s", got, want, view(a))
	}
	if !strings.Contains(plainLines(a)[1], "largest first") {
		t.Errorf("header does not say the list is shown largest first: %q", plainLines(a)[1])
	}

	press(a, 's')
	if got, want := order(t, a, "zeta", "a-two", "a-one", "a-new"), []string{"a-one", "a-new", "a-two", "zeta"}; !slices.Equal(got, want) {
		t.Errorf("s again shows %q, want the order found %q:\n%s", got, want, view(a))
	}
	if strings.Contains(view(a), "largest first") {
		t.Errorf("header still says largest first:\n%s", view(a))
	}
}

// Sorting moves rows but never moves the cursor to another worktree. d acts
// on the same row before and after, even the first row the user has not
// moved from.
func TestSortKeepsTheCursorOnItsWorktree(t *testing.T) {
	a := testApp()
	a.Update(tea.WindowSizeMsg{Width: 100, Height: 20})
	sized(a, "small", "/r/one", 10)
	sized(a, "large", "/r/two", 1000)

	press(a, 's')
	if got := statusLine(plainLines(a)); got != "/w/small" {
		t.Errorf("sorting moved the cursor from small to %q", got)
	}
}

// Removing worktrees changes the totals, so a list shown largest first is
// ranked again.
func TestLargestFirstAfterRemoving(t *testing.T) {
	a, _ := removalApp(t)
	sized(a, "a-big", "/r/alpha", 1000)
	sized(a, "a-small", "/r/alpha", 10)
	sized(a, "b-mid", "/r/beta", 500)
	a.Update(eventMsg{ev: engine.ScanDone{}})
	press(a, 's')

	press(a, 'd') // a-big, under the cursor
	settle(a, press(a, tea.KeyEnter))
	press(a, tea.KeyEscape)
	if got, want := order(t, a, "alpha", "beta"), []string{"beta", "alpha"}; !slices.Equal(got, want) {
		t.Errorf("after removing a-big, largest first shows %q, want beta (500 B) above alpha (10 B):\n%s", got, view(a))
	}
}

// Shown largest first while the scan goes on, the list is re-ranked
// once a second, not on every size, and the row under the cursor stays
// on its screen line as rows land above it; the end of the scan ranks
// it for the last time.
func TestLargestFirstReranksAroundTheCursor(t *testing.T) {
	a := testApp()
	a.Update(tea.WindowSizeMsg{Width: 100, Height: 10}) // four lines of list
	for i := 1; i <= 8; i++ {
		sized(a, fmt.Sprintf("r%d", i), "/r", int64(900-100*i)) // r1 800 B … r8 100 B
	}
	press(a, 's')
	for range 6 {
		press(a, tea.KeyDown)
	}
	view(a)             // r7 on the last line shown
	press(a, tea.KeyUp) // r6, above it
	lines := plainLines(a)
	at := lineWith(t, lines, "r6")

	sized(a, "r9", "/r", 850)  // above r1
	sized(a, "r10", "/r", 250) // between r6 and r7
	if lines := plainLines(a); lineWith(t, lines, "r6") != at || !strings.Contains(lines[at+1], "r7") {
		t.Errorf("list changed before its re-ranking:\n%s", strings.Join(lines, "\n"))
	}

	a.Update(rankMsg{a.ranking})
	lines = plainLines(a)
	if now := lineWith(t, lines, "r6"); now != at {
		t.Errorf("cursor row moved from line %d to %d when rows landed above it:\n%s", at, now, strings.Join(lines, "\n"))
	}
	if !strings.Contains(lines[at+1], "r10") {
		t.Errorf("r10 (250 B) is not ranked below r6 (300 B):\n%s", strings.Join(lines, "\n"))
	}
	if got := statusLine(lines); got != "/w/r6" {
		t.Errorf("cursor left r6: status line shows %q", got)
	}

	sized(a, "r11", "/r", 275)
	a.Update(eventMsg{ev: engine.ScanDone{}})
	if lines := plainLines(a); !strings.Contains(lines[at+1], "r11") {
		t.Errorf("the end of the scan did not rank r11 (275 B) below r6 (300 B):\n%s", strings.Join(lines, "\n"))
	}
}

// search reports three worktrees: feature/login, safe to delete, and
// fix/typo, with uncommitted work, in app; chore/deps, with unpushed
// commits, in api.
func searchApp() *app {
	a := testApp()
	a.Update(tea.WindowSizeMsg{Width: 100, Height: 20})
	for _, wt := range []struct {
		id, repo string
		facts    lopper.Facts
		safe     bool
	}{
		{"feature/login", "/r/app", clean(), true},
		{"fix/typo", "/r/app", lopper.Facts{Dirty: new(3), UncheckedFiles: new(0), Unpushed: new(0), Merged: new(lopper.NotMerged)}, false},
		{"chore/deps", "/r/api", lopper.Facts{Dirty: new(0), UncheckedFiles: new(0), Unpushed: new(2), Merged: new(lopper.NotMerged)}, false},
	} {
		a.Update(eventMsg{ev: engine.WorktreeFound{Worktree: lopper.Worktree{
			ID: lopper.ID(wt.id), Path: "/w/" + wt.id, Branch: wt.id, Repo: lopper.Repo{Path: wt.repo},
		}}})
		a.Update(eventMsg{ev: engine.FactsUpdated{ID: lopper.ID(wt.id), Facts: wt.facts, Safe: wt.safe}})
	}
	return a
}

// / shows only the rows matching every word typed, by branch, path or fact.
// A fact matches without its count and not inside another fact, so "merged"
// leaves out "not merged". esc shows every row again.
func TestSearchShowsMatchingRows(t *testing.T) {
	all := []string{"feature/login", "fix/typo", "chore/deps"}
	for _, tc := range []struct {
		query string
		want  []string
	}{
		{"merged", []string{"feature/login"}},
		{"safe", []string{"feature/login"}},
		{"unpushed", []string{"chore/deps"}},
		{"APP not", []string{"fix/typo"}},
		{"nothing", nil},
	} {
		t.Run(tc.query, func(t *testing.T) {
			a := searchApp()
			typeText(a, "/"+tc.query)
			v := view(a)
			for _, branch := range all {
				if shown := strings.Contains(v, branch); shown != slices.Contains(tc.want, branch) {
					t.Errorf("%s shown: %v, want %v:\n%s", branch, shown, !shown, v)
				}
			}
			if !strings.Contains(plainLines(a)[1], "/ "+tc.query) {
				t.Errorf("header does not show the query: %q", plainLines(a)[1])
			}
			press(a, tea.KeyEscape)
			for _, branch := range all {
				if !strings.Contains(view(a), branch) {
					t.Errorf("esc did not show %s again:\n%s", branch, view(a))
				}
			}
		})
	}
}

// While searching, letters go into the query rather than act as keys;
// enter ends the search and keeps its rows, and the keys act again.
func TestKeysTypedIntoTheSearch(t *testing.T) {
	a := searchApp()
	typeText(a, "/deps") // d removes, s sorts
	v := view(a)
	if strings.Contains(v, "Remove") || strings.Contains(v, "largest first") || !strings.Contains(plainLines(a)[1], "/ deps") {
		t.Errorf("letters typed into the search acted as keys:\n%s", v)
	}
	press(a, tea.KeyEnter)
	press(a, 's')
	v = view(a)
	if !strings.Contains(v, "largest first") || strings.Contains(v, "feature/login") {
		t.Errorf("after enter, s does not sort the rows found:\n%s", v)
	}
}

// Rows hidden by the search stay selected. The footer counts them, and
// removing acts on them, as the confirmation shows.
func TestSelectionHiddenBySearchIsCounted(t *testing.T) {
	a := searchApp()
	press(a, tea.KeyDown)
	press(a, ' ') // fix/typo
	typeText(a, "/login")
	press(a, tea.KeyEnter)
	lines := plainLines(a)
	if got := statusLine(lines); !strings.Contains(got, "1 selected (1 hidden)") {
		t.Errorf("status line does not count the selected row the search hides: %q", got)
	}
	if !strings.Contains(lines[lineWith(t, lines, "app")], "1 of 2 worktrees") {
		t.Errorf("repository header does not say a row is hidden: %q", lines[lineWith(t, lines, "app")])
	}
	press(a, 'd')
	if v := view(a); !strings.Contains(v, "fix/typo") || strings.Contains(v, "feature/login") {
		t.Errorf("remove does not act on the hidden selected row:\n%s", v)
	}
}

// The key help shows the main keys, ending with ?, which stays however
// narrow the screen.
func TestKeyHelpEndsWithQuestionMark(t *testing.T) {
	a := searchApp()
	a.Update(tea.WindowSizeMsg{Width: 100, Height: 12})
	if v := view(a); strings.Contains(v, "↑/k") || !strings.Contains(v, "? more keys") {
		t.Errorf("key help is not the main keys:\n%s", v)
	}
	a.Update(tea.WindowSizeMsg{Width: 40, Height: 12})
	if keys := plainLines(a)[len(plainLines(a))-1]; !strings.HasSuffix(strings.TrimSpace(keys), "? more keys") {
		t.Errorf("on a narrow screen, ? gave way to other keys: %q", keys)
	}
}

// ? shows every key in place of the list, within the screen however small.
// Where they do not fit, ↓ scrolls to the rest. ? or esc goes back to the
// list.
func TestQuestionMarkShowsEveryKey(t *testing.T) {
	every := []string{"↑/k up", "↓/j down", "space select", "d remove", "/ search", "s sort", "q quit"}
	for _, size := range []tea.WindowSizeMsg{{Width: 100, Height: 12}, {Width: 28, Height: 12}} {
		t.Run(fmt.Sprintf("%dx%d", size.Width, size.Height), func(t *testing.T) {
			a := searchApp()
			a.Update(size)
			press(a, '?')
			var frames []string
			for range len(every) {
				lines := plainLines(a)
				if len(lines) > size.Height {
					t.Fatalf("%d lines on a %d-line screen:\n%s", len(lines), size.Height, view(a))
				}
				if keys := strings.TrimSpace(lines[len(lines)-1]); !strings.HasSuffix(keys, "? close") {
					t.Errorf("key help does not say how to close the keys: %q", keys)
				}
				frames = append(frames, strings.Join(strings.Fields(view(a)), " "))
				press(a, tea.KeyDown)
			}
			seen := strings.Join(frames, "\n")
			for _, want := range every {
				if !strings.Contains(seen, want) {
					t.Errorf("scrolling through every key never shows %q:\n%s", want, seen)
				}
			}
			if strings.Contains(seen, "fix/typo") {
				t.Errorf("rows show beside every key:\n%s", seen)
			}

			press(a, '?')
			if v := view(a); strings.Contains(v, "↑/k") || !strings.Contains(v, "fix/typo") {
				t.Errorf("? again does not go back to the list:\n%s", v)
			}
		})
	}
}

// Removing a selected row the search hides leaves the cursor on the row
// it shows, which the next d acts on.
func TestRemovingAHiddenRowKeepsTheCursor(t *testing.T) {
	a, _ := removalApp(t)
	sized(a, "match-a", "/r", 30)
	sized(a, "match-b", "/r", 20)
	sized(a, "hidden", "/r", 10)
	press(a, tea.KeyDown)
	press(a, tea.KeyDown)
	press(a, ' ') // hidden and last, so the cursor stays on it
	typeText(a, "/match")
	press(a, tea.KeyEnter)
	if got := statusLine(plainLines(a)); !strings.HasPrefix(got, "/w/match-a ") {
		t.Fatalf("cursor shows %q, want /w/match-a", got)
	}

	press(a, 'd')
	settle(a, press(a, tea.KeyEnter))
	press(a, tea.KeyEscape)
	if got := statusLine(plainLines(a)); got != "/w/match-a" {
		t.Errorf("removing the hidden row moved the cursor to %q", got)
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
	a.Update(eventMsg{ev: engine.FactsUpdated{
		ID:    "wt",
		Facts: lopper.Facts{Dirty: new(3), UncheckedFiles: new(0), Unpushed: new(2), Merged: new(lopper.NotMerged)},
	}})

	a.Update(tea.WindowSizeMsg{Width: 100, Height: 20})
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

// A row shows all its facts when the screen has room, and otherwise the
// most pressing ones and how many more there are. A fact that makes a
// worktree unsafe must never vanish without a trace.
func TestRowShowsAllFactsOrCountsTheRest(t *testing.T) {
	a := testApp()
	a.Update(eventMsg{ev: engine.WorktreeFound{Worktree: lopper.Worktree{
		ID: "wt", Path: "/w/wt", Branch: "fix/login", State: lopper.StateUnconfirmed, Reason: "HEAD is missing",
	}}})
	a.Update(eventMsg{ev: engine.FactsUpdated{
		ID: "wt", Facts: lopper.Facts{Dirty: new(3), UncheckedFiles: new(0), Unpushed: new(0), Merged: new(lopper.MergedFF)},
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

func TestNarrowRowShowsUncheckedFilesBeforeMerged(t *testing.T) {
	a := testApp()
	a.Update(eventMsg{ev: engine.WorktreeFound{Worktree: lopper.Worktree{
		ID: "wt", Path: "/w/wt", Branch: "fix/login",
	}}})
	a.Update(eventMsg{ev: engine.FactsUpdated{
		ID: "wt", Facts: lopper.Facts{Dirty: new(0), UncheckedFiles: new(2), Unpushed: new(0), Merged: new(lopper.MergedFF)},
	}})
	a.Update(tea.WindowSizeMsg{Width: 50, Height: 20})
	lines := plainLines(a)
	line := lines[lineWith(t, lines, "fix/login")]
	if !strings.Contains(line, "2 unchecked +1") {
		t.Errorf("narrow row hides the unchecked files: %q", line)
	}
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
	measured := clean()
	measured.SizeBytes = new(int64(1000))
	for _, id := range []string{"measured", "measuring"} {
		a.Update(eventMsg{ev: engine.WorktreeFound{Worktree: lopper.Worktree{ID: lopper.ID(id), Branch: "a-branch-long-enough-to-be-cut-" + id}}})
	}
	a.Update(eventMsg{ev: engine.FactsUpdated{ID: "measured", Facts: measured, Safe: true}})
	a.Update(eventMsg{ev: engine.FactsUpdated{ID: "measuring", Facts: clean(), Safe: true}})

	lines := plainLines(a)
	at := lineWith(t, lines, "a-branch") // measured first, since it is larger
	known, unknown := lines[at], strings.TrimRight(lines[at+1], " ")
	if !strings.Contains(known, "1.0 kB") || !strings.HasSuffix(unknown, spinning(a)) {
		t.Fatalf("want the measured row, then the one still being measured:\n%s\n%s", known, unknown)
	}
	if strings.Index(known, "merged") != strings.Index(unknown, "merged") {
		t.Errorf("status column moves on a row with unknown size:\n%s\n%s", known, unknown)
	}
}

// spinning is the spinner's current frame, which a size being measured shows.
func spinning(a *app) string { return strings.TrimSpace(a.spin.View()) }

// A size that could not be measured must not look as if it still were, or
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
	bg = bg[strings.Index(bg, "48;"):strings.Index(bg, "m")] // the background's parameters
	return strings.Contains(rawLine(a, text), bg+"m")
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

// Scrolling assumes one screen line per line of the view, so on a narrow
// terminal every line must still fit, and a row must keep its size.
func TestNarrowScreenKeepsEveryLineWithinWidth(t *testing.T) {
	a := testApp()
	a.Update(tea.WindowSizeMsg{Width: 40, Height: 20})
	repo := lopper.Repo{Path: "/home/me/code/a-repository-with-a-long-name"}
	a.Update(eventMsg{ev: engine.WorktreeFound{Worktree: lopper.Worktree{
		ID: "wt", Path: repo.Path + "/.claude/worktrees/x", Branch: "feature/a-branch-name-longer-than-the-screen", Repo: repo,
	}}})
	a.Update(eventMsg{ev: engine.FactsUpdated{
		ID:    "wt",
		Facts: lopper.Facts{Dirty: new(3), UncheckedFiles: new(0), Unpushed: new(2), Merged: new(lopper.NotMerged), SizeBytes: new(int64(1_200_000_000))},
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
