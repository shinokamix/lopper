// Package tui is the interactive front end. It follows The Elm
// Architecture: engine events and key presses become messages, Update
// mutates state, View renders it.
package tui

import (
	"context"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/shinokamix/lopper/internal/engine"
)

type eventMsg struct{ ev engine.Event }

type app struct {
	events <-chan engine.Event
	store  *store
	keys   keyMap
	theme  theme
	help   help.Model
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
		list:   newList(),
	}
	_, err := tea.NewProgram(a, tea.WithContext(ctx)).Run()
	return err
}

func (a *app) Init() tea.Cmd {
	return tea.Batch(a.waitEvent(), tea.RequestBackgroundColor)
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

func (a *app) View() tea.View {
	body := a.list.view(a.theme, a.store, a.width, a.height)
	v := tea.NewView(body + "\n" + a.help.View(a.keys))
	v.AltScreen = true
	return v
}
