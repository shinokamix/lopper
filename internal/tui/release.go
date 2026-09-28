package tui

import (
	"context"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
)

// Updates lets the TUI offer a newer release of lopper before it scans.
// The zero value offers none.
type Updates struct {
	Current string // the running version
	Repo    string // where release notes are, under releases/tag/<tag>
	// Check returns a release newer than Current, or "".
	Check func(context.Context) string
	// Install puts release tag in place of the running binary.
	Install func(ctx context.Context, tag string) error
}

// releaseMsg reports what the check found: a newer release, or "".
type releaseMsg struct{ tag string }

// installedMsg reports how installing the newer release went.
type installedMsg struct{ err error }

// offer is the screen shown before the scan while a newer release is out:
// it offers the release, installs it, then tells how that went.
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

func (a *app) checkRelease() tea.Cmd {
	check, ctx := a.updates.Check, a.ctx
	return func() tea.Msg { return releaseMsg{check(ctx)} }
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
	case o.phase == offered && key.Matches(msg, a.keys.install):
		return a.install()
	case o.phase == offered && key.Matches(msg, a.keys.skip),
		o.phase == installed && key.Matches(msg, a.keys.proceed):
		return a.startScanning()
	}
	return nil
}

func (o *offer) view(t theme, h help.Model, k keyMap, u Updates, spin string, width int) string {
	title := "Update available: lopper " + o.tag
	var body string
	var keys bindings
	switch o.phase {
	case offered:
		body = "You have " + u.Current + ". What's new: " + u.Repo + "/releases/tag/" + o.tag
		if o.err != nil {
			body += "\n\n " + t.failure.Width(max(width-1, 1)).Render("update failed: "+o.err.Error())
		}
		keys = bindings{k.install, k.skip, k.quit}
	case installing:
		body = spin + " installing…"
	case installed:
		title = "Updated to lopper " + o.tag
		body = "Run lopper again to use it."
		keys = bindings{k.proceed, k.quit}
	}
	h.SetWidth(max(width-2, 0))
	return " " + t.title.Render(title) + "\n\n " + body + "\n\n " + h.View(keys)
}
