// Package tui is the interactive front end. It follows The Elm
// Architecture: engine events and key presses become messages, Update
// mutates state, View renders it.
package tui

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/shinokamix/lopper/internal/engine"
)

type eventMsg struct{ ev engine.Event }

type app struct {
	events <-chan engine.Event
	store  *store
	keys   keyMap
	theme  theme
	help   help.Model
	spin   spinner.Model
	list   list
	width  int
	height int
}

// Run starts the TUI and a scan feeding it.
func Run(ctx context.Context, eng *engine.Engine, opts engine.Options) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	a := &app{
		events: eng.Scan(ctx, opts),
		store:  newStore(),
		keys:   defaultKeys(),
		theme:  newTheme(true),
		help:   help.New(),
		spin:   spinner.New(spinner.WithSpinner(spinner.Dot)),
		list:   newList(pathAliases()),
	}
	_, err := tea.NewProgram(a, tea.WithContext(ctx)).Run()
	return err
}

func (a *app) Init() tea.Cmd {
	return tea.Batch(a.waitEvent(), tea.RequestBackgroundColor, a.spin.Tick)
}

// waitEvent bridges the engine channel into Bubble Tea messages.
// TODO: coalesce bursts of events into one message per frame.
func (a *app) waitEvent() tea.Cmd {
	return func() tea.Msg {
		ev, ok := <-a.events
		if !ok {
			return nil
		}
		return eventMsg{ev}
	}
}

func (a *app) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case eventMsg:
		a.store.apply(msg.ev)
		next := a.waitEvent()
		return a, next
	case spinner.TickMsg:
		if !a.store.scanning {
			return a, nil // stop ticking
		}
		var cmd tea.Cmd
		a.spin, cmd = a.spin.Update(msg)
		return a, cmd
	case tea.BackgroundColorMsg:
		a.theme = newTheme(msg.IsDark())
		a.help.Styles = help.DefaultStyles(msg.IsDark())
	case tea.WindowSizeMsg:
		a.width, a.height = msg.Width, msg.Height
	case tea.KeyPressMsg:
		if key.Matches(msg, a.keys.quit) {
			return a, tea.Quit
		}
		a.list.update(msg, a.keys, a.store)
	}
	return a, nil
}

// Screen layout, inside a margin: a blank line, the header and a blank
// line, the list, then a blank line, the status line and
// the key help.
const (
	chromeLines = 6
	margin      = 2
)

func (a *app) View() tea.View {
	w := max(a.width-2*margin, 1)
	body := a.list.view(a.theme, a.store, w, max(a.height-chromeLines, 1))
	screen := header(a.theme, a.store, a.spin.View(), w) + "\n\n" +
		body + "\n\n" + a.list.footer(a.theme, a.store, a.help, a.keys, w)
	// Scrolling counts one screen line per line: a line wider than the
	// screen would wrap and push the list down, so none may be.
	lines := strings.Split(screen, "\n")
	for i, l := range lines {
		lines[i] = strings.Repeat(" ", margin) + ansi.Truncate(l, w, "…")
	}
	v := tea.NewView("\n" + strings.Join(lines, "\n"))
	v.AltScreen = true
	return v
}

// pathAliases shortens paths under the home directory to ~ and under the
// temporary directories, where agents often put worktrees, to tmp.
func pathAliases() []alias {
	var out []alias
	add := func(dir, name string) {
		if dir == "" {
			return
		}
		out = append(out, alias{dir, name})
		if resolved, err := filepath.EvalSymlinks(dir); err == nil && resolved != dir {
			out = append(out, alias{resolved, name}) // e.g. /tmp is /private/tmp on macOS
		}
	}
	if home, err := os.UserHomeDir(); err == nil {
		add(home, "~")
	}
	add(os.TempDir(), "tmp")
	if runtime.GOOS != "windows" {
		add("/tmp", "tmp")
	}
	slices.SortFunc(out, func(a, b alias) int { return len(b.dir) - len(a.dir) })
	return out
}
