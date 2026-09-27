package tui

import "charm.land/bubbles/v2/key"

// keyMap defines every key binding; help text is generated from it.
type keyMap struct {
	up, down, toggle, quit key.Binding
}

func defaultKeys() keyMap {
	return keyMap{
		up:     key.NewBinding(key.WithKeys("up", "k"), key.WithHelp("↑/k", "up")),
		down:   key.NewBinding(key.WithKeys("down", "j"), key.WithHelp("↓/j", "down")),
		toggle: key.NewBinding(key.WithKeys("space"), key.WithHelp("space", "select")),
		quit:   key.NewBinding(key.WithKeys("q", "ctrl+c"), key.WithHelp("q", "quit")),
	}
}

func (k keyMap) ShortHelp() []key.Binding  { return []key.Binding{k.up, k.down, k.toggle, k.quit} }
func (k keyMap) FullHelp() [][]key.Binding { return [][]key.Binding{k.ShortHelp()} }
