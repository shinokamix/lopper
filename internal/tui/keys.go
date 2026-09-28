package tui

import "charm.land/bubbles/v2/key"

// keyMap defines every key binding; help text is generated from it.
type keyMap struct {
	up, down, toggle, remove, quit key.Binding
	// On the removal screen.
	scroll, confirm, stop, rescan, back key.Binding
	// On the update screen.
	install, skip, proceed key.Binding
}

func defaultKeys() keyMap {
	return keyMap{
		up:      key.NewBinding(key.WithKeys("up", "k"), key.WithHelp("↑/k", "up")),
		down:    key.NewBinding(key.WithKeys("down", "j"), key.WithHelp("↓/j", "down")),
		toggle:  key.NewBinding(key.WithKeys("space"), key.WithHelp("space", "select")),
		remove:  key.NewBinding(key.WithKeys("d"), key.WithHelp("d", "remove")),
		quit:    key.NewBinding(key.WithKeys("q", "ctrl+c"), key.WithHelp("q", "quit")),
		scroll:  key.NewBinding(key.WithKeys("up", "k", "down", "j"), key.WithHelp("↑/↓", "scroll")),
		confirm: key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "remove")),
		stop:    key.NewBinding(key.WithKeys("q", "ctrl+c"), key.WithHelp("q", "stop after this one")),
		rescan:  key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "scan again")),
		back:    key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "back")),
		install: key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "update")),
		skip:    key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "skip")),
		proceed: key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "scan with this version")),
	}
}

func (k keyMap) ShortHelp() []key.Binding {
	return []key.Binding{k.up, k.down, k.toggle, k.remove, k.quit}
}
func (k keyMap) FullHelp() [][]key.Binding { return [][]key.Binding{k.ShortHelp()} }

// bindings is key help for a screen other than the list.
type bindings []key.Binding

func (b bindings) ShortHelp() []key.Binding  { return b }
func (b bindings) FullHelp() [][]key.Binding { return [][]key.Binding{b} }
