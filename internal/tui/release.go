package tui

import (
	"context"
	"errors"
	"io/fs"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
)

// Updates lets the TUI offer a newer release of lopper before it scans.
// The zero value offers none.
type Updates struct {
	Current string // the running version
	Latest  string // a newer release to offer, or ""
	Repo    string // where release notes are, under releases/tag/<tag>
	// Install puts release tag in place of the running binary.
	Install func(ctx context.Context, tag string) error
	// Skip keeps release tag from being offered again.
	Skip func(tag string)
	// Restart runs the installed release in place of this process. It
	// returns only if it fails.
	Restart func() error
}

// installedMsg reports how installing the newer release went.
type installedMsg struct{ err error }

// offer is the screen shown before the scan while a newer release is out:
// it offers the release, installs it, then offers to restart into it.
type offer struct {
	tag   string
	phase offerPhase
	err   error // why the last install failed
}

type offerPhase int

const (
	offered offerPhase = iota
	installing
	installed
)

// retryable reports whether installing again may succeed: not where
// lopper may not write, which the error says how to get around.
func (o *offer) retryable() bool {
	return !errors.Is(o.err, fs.ErrPermission)
}

func (a *app) install() tea.Cmd {
	install, ctx, tag := a.updates.Install, a.ctx, a.offer.tag
	a.offer.phase, a.offer.err = installing, nil
	return tea.Batch(func() tea.Msg { return installedMsg{install(ctx, tag)} }, a.spin.Tick)
}

func (a *app) offerKey(msg tea.KeyPressMsg) tea.Cmd {
	switch o := a.offer; {
	case key.Matches(msg, a.keys.quit):
		return tea.Quit
	case o.phase == offered && o.retryable() && key.Matches(msg, a.keys.install):
		return a.install()
	case o.phase == offered && key.Matches(msg, a.keys.skip):
		a.updates.Skip(o.tag)
		return a.startScanning()
	case o.phase == installed && key.Matches(msg, a.keys.restart):
		a.restart = true
		return tea.Quit
	}
	return nil
}

func (o *offer) view(t theme, h help.Model, k keyMap, u Updates, spin string, width int) string {
	title := "Update available: lopper " + u.Current + " → " + o.tag
	var body string
	var keys bindings
	switch o.phase {
	case offered:
		notes := u.Repo + "/releases/tag/" + o.tag
		body = "What's new:\n " + t.subtle.Hyperlink(notes).Render(notes)
		keys = bindings{k.install, k.skip, k.quit}
		if o.err != nil {
			body += "\n\n " + t.failure.Width(max(width-1, 1)).Render("update failed: "+o.err.Error())
			if !o.retryable() {
				keys = bindings{k.skip, k.quit}
			}
		}
	case installing:
		body = spin + " installing…"
	case installed:
		title = "Updated to lopper " + o.tag
		body = "Restart lopper to use it."
		keys = bindings{k.restart, k.quit}
	}
	h.SetWidth(max(width-2, 0))
	return " " + t.title.Render(title) + "\n\n " + body + "\n\n " + h.View(keys)
}
