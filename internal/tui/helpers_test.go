package tui

import (
	"strings"
	"testing"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/shinokamix/lopper/internal/lopper"
)

func testApp() *app {
	return &app{store: newStore(), keys: defaultKeys(), theme: newTheme(true), help: help.New(), spin: spinner.New(), list: newList([]alias{{"/home/me", "~"}})}
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

// statusLine is the line above the key help: the path under the cursor.
func statusLine(lines []string) string {
	return strings.TrimSpace(lines[len(lines)-2])
}

// clean is what the scan finds in a worktree it reports safe: merged,
// with nothing uncommitted, unchecked or unpushed.
func clean() lopper.Facts {
	return lopper.Facts{Dirty: new(0), UncheckedFiles: new(0), Unpushed: new(0), Merged: new(lopper.MergedFF)}
}
