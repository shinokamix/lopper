package tui

import "charm.land/bubbles/v2/key"

// keyMap defines every key binding, and the help text comes from it.
type keyMap struct {
	up, down, toggle, remove, quit key.Binding
	// On the list: its order, its search and its key help.
	sort, search, help key.Binding
	// While searching.
	done, clear, move key.Binding
	// On the removal screen.
	scroll, confirm, stop, rescan, back key.Binding
	// On the update screen.
	install, later, skip, restart key.Binding
}

func defaultKeys() keyMap {
	return keyMap{
		up:      key.NewBinding(key.WithKeys("up", "k"), key.WithHelp("↑/k", "up")),
		down:    key.NewBinding(key.WithKeys("down", "j"), key.WithHelp("↓/j", "down")),
		toggle:  key.NewBinding(key.WithKeys("space"), key.WithHelp("space", "select")),
		remove:  key.NewBinding(key.WithKeys("d"), key.WithHelp("d", "remove")),
		quit:    key.NewBinding(key.WithKeys("q", "ctrl+c"), key.WithHelp("q", "quit")),
		sort:    key.NewBinding(key.WithKeys("s"), key.WithHelp("s", "sort")),
		search:  key.NewBinding(key.WithKeys("/"), key.WithHelp("/", "search")),
		help:    key.NewBinding(key.WithKeys("?"), key.WithHelp("?", "more keys")),
		done:    key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "done")),
		clear:   key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "clear search")),
		move:    key.NewBinding(key.WithKeys("up", "down"), key.WithHelp("↑/↓", "move")),
		scroll:  key.NewBinding(key.WithKeys("up", "k", "down", "j"), key.WithHelp("↑/↓", "scroll")),
		confirm: key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "remove")),
		stop:    key.NewBinding(key.WithKeys("q", "ctrl+c"), key.WithHelp("q", "stop after this one")),
		rescan:  key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "scan again")),
		back:    key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "back")),
		install: key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "update")),
		later:   key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "not now")),
		skip:    key.NewBinding(key.WithKeys("s"), key.WithHelp("s", "skip this version")),
		restart: key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "restart")),
	}
}

// bindings is a set of keys to show help for.
type bindings []key.Binding

func (b bindings) ShortHelp() []key.Binding  { return b }
func (b bindings) FullHelp() [][]key.Binding { return [][]key.Binding{b} }
