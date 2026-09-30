package tui

import (
	"context"
	"errors"
	"io/fs"
	"strings"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
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
	// quitting is set to quit once the release is installed.
	quitting bool
	// done is closed once the last install has returned.
	done chan struct{}
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

// waitInstall returns once no install runs. On Windows, Install moves
// lopper away before moving the new binary into its place: exiting
// between the two would leave neither, so nothing may end the process
// while it runs, not even a signal that ends the TUI.
func (o *offer) waitInstall() {
	if o != nil && o.done != nil {
		<-o.done
	}
}

func (a *app) install() tea.Cmd {
	install, ctx, tag := a.updates.Install, a.ctx, a.offer.tag
	done := make(chan struct{})
	a.offer.phase, a.offer.err, a.offer.done = installing, nil, done
	return tea.Batch(func() tea.Msg {
		defer close(done)
		return installedMsg{install(ctx, tag)}
	}, a.spin.Tick)
}

func (a *app) offerKey(msg tea.KeyPressMsg) tea.Cmd {
	switch o := a.offer; {
	case o.phase == installing:
		// Quit once installed, as quitting now would leave lopper waiting
		// for it with nothing on the screen.
		o.quitting = o.quitting || key.Matches(msg, a.keys.quitLater)
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

// view renders the screen in width and height, keeping the key help
// whole: without it, there is no telling how to go on.
func (o *offer) view(t theme, h help.Model, k keyMap, u Updates, spin string, width, height int) string {
	w := max(width-1, 1) // each line is indented by one
	wrap := func(s string) []string { return strings.Split(ansi.Wrap(s, w, ""), "\n") }
	text := append(wrap(t.title.Render("Update available: lopper "+u.Current+" → "+o.tag)), "")
	var keys bindings
	switch o.phase {
	case offered:
		if o.err != nil {
			text = append(append(text, wrap(t.failure.Render("update failed: "+o.err.Error()))...), "")
		}
		// The link is not wrapped: cut short, it still leads there.
		notes := u.Repo + "/releases/tag/" + o.tag
		text = append(text, "What's new:", t.subtle.Hyperlink(notes).Render(notes))
		keys = bindings{k.install, k.skip, k.quit}
		if !o.retryable() {
			keys = bindings{k.skip, k.quit}
		}
	case installing:
		if o.quitting {
			text = append(text, wrap(spin+" installing… quitting after it")...)
		} else {
			text = append(text, wrap(spin+" installing…")...)
			keys = bindings{k.quitLater}
		}
	case installed:
		text = append(wrap(t.title.Render("Updated to lopper "+o.tag)), "")
		text = append(text, wrap("Restart lopper to use it.")...)
		keys = bindings{k.restart, k.quit}
	}

	keyHelp := helpLines(h, keys, w)
	if room := height - len(keyHelp) - 1; len(text) > room {
		text = text[:max(room, 0)]
		for len(text) > 0 && text[len(text)-1] == "" {
			text = text[:len(text)-1]
		}
	}
	lines := text
	if len(text) > 0 && len(keyHelp) > 0 {
		lines = append(lines, "")
	}
	return " " + strings.Join(append(lines, keyHelp...), "\n ")
}

// helpLines renders keys on one line of width if they fit, and one key
// per line otherwise, rather than cut any off.
func helpLines(h help.Model, keys bindings, width int) []string {
	if len(keys) == 0 {
		return nil
	}
	h.SetWidth(0) // cutting off is what this avoids
	if line := h.ShortHelpView(keys); ansi.StringWidth(line) <= width {
		return []string{line}
	}
	lines := make([]string, len(keys))
	for i, b := range keys {
		lines[i] = h.ShortHelpView([]key.Binding{b})
	}
	return lines
}
