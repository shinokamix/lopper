package tui

import (
	"context"
	"errors"
	"io/fs"
	"slices"
	"strings"
	"sync"

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
	// Postpone keeps release tag from being offered for a while, and Skip
	// from being offered again.
	Postpone, Skip func(tag string)
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
	// cancel stops the install while it downloads.
	cancel context.CancelFunc
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

// installs lets Run wait for the install that runs when the TUI quits.
// On Windows, Install moves lopper away before moving the new binary into
// its place: exiting between the two would leave neither, so nothing may
// end the process while it runs, not even a signal that ends the TUI.
type installs struct {
	mu      sync.Mutex
	closed  bool
	running sync.WaitGroup
}

// start reports whether an install may start: not once the TUI quit,
// which may drop the command running it before it does.
func (s *installs) start() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return false
	}
	s.running.Add(1)
	return true
}

// close keeps any install from starting and waits for the one running.
func (s *installs) close() {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
	s.running.Wait()
}

// install returns the command installing the offered release.
func (a *app) install() tea.Cmd {
	ctx, cancel := context.WithCancel(a.ctx)
	install, tag, gate := a.updates.Install, a.offer.tag, &a.installs
	a.offer.phase, a.offer.err, a.offer.cancel = installing, nil, cancel
	return func() tea.Msg {
		defer cancel()
		if !gate.start() {
			return nil
		}
		defer gate.running.Done()
		return installedMsg{install(ctx, tag)}
	}
}

func (a *app) offerKey(msg tea.KeyPressMsg) tea.Cmd {
	switch o := a.offer; {
	case key.Matches(msg, a.keys.quit):
		if o.phase == installing {
			// Stops the download, so that exiting waits at most for the
			// binary being put in place.
			o.cancel()
		}
		return tea.Quit
	case o.phase == offered && o.retryable() && key.Matches(msg, a.keys.install):
		return tea.Batch(a.install(), a.spin.Tick)
	case o.phase == offered && key.Matches(msg, a.keys.later):
		a.updates.Postpone(o.tag)
		return a.startScanning()
	case o.phase == offered && key.Matches(msg, a.keys.skip):
		a.updates.Skip(o.tag)
		return a.startScanning()
	case o.phase == installed && key.Matches(msg, a.keys.restart):
		a.restart = true
		return tea.Quit
	}
	return nil
}

// view renders the screen in width and height. The key help is kept
// whole, as without it there is no telling how to go on; of the rest,
// what matters least goes first when it does not fit.
func (o *offer) view(t theme, h help.Model, k keyMap, u Updates, spin string, width, height int) string {
	w := max(width-1, 1) // each line is indented by one
	wrap := func(s string) []string { return strings.Split(ansi.Wrap(s, w, ""), "\n") }
	title := wrap(t.title.Render("Update available: lopper " + u.Current + " → " + o.tag))
	var parts []part
	var keys bindings
	switch o.phase {
	case offered:
		parts = append(parts, part{title, 2})
		if o.err != nil {
			// Its end may say how to update instead: kept when cut short.
			parts = append(parts, part{wrap(t.failure.Render("update failed: " + o.err.Error())), 1})
		}
		// The link is not wrapped: cut short, it still leads there.
		notes := u.Repo + "/releases/tag/" + o.tag
		parts = append(parts, part{[]string{"What's new:", t.subtle.Hyperlink(notes).Render(notes)}, 3})
		keys = bindings{k.install, k.later, k.skip, k.quit}
		if !o.retryable() {
			keys = bindings{k.later, k.skip, k.quit}
		}
	case installing:
		parts = []part{{title, 2}, {wrap(spin + " installing…"), 1}}
		keys = bindings{k.quit}
	case installed:
		parts = []part{{wrap(t.title.Render("Updated to lopper " + o.tag)), 1}, {wrap("Restart lopper to use it."), 2}}
		keys = bindings{k.restart, k.quit}
	}

	keyHelp := helpLines(h, keys, w)
	room := height
	if len(keyHelp) > 0 {
		room -= len(keyHelp) + 1 // and a blank line above
	}
	var lines []string
	for _, p := range fitParts(parts, room, w) {
		if len(lines) > 0 {
			lines = append(lines, "")
		}
		lines = append(lines, p.lines...)
	}
	if len(lines) > 0 && len(keyHelp) > 0 {
		lines = append(lines, "")
	}
	return " " + strings.Join(append(lines, keyHelp...), "\n ")
}

// part is a paragraph of the update screen, and how much it matters: 1
// most.
type part struct {
	lines []string
	rank  int
}

// fitParts drops the parts that matter least until the rest fit in height
// lines, blank lines between them included. The last one left, if still
// too long, keeps its first line and its end, which of an error says
// what to do about it.
func fitParts(parts []part, height, width int) []part {
	size := func() int {
		n := len(parts) - 1
		for _, p := range parts {
			n += len(p.lines)
		}
		return n
	}
	for len(parts) > 1 && size() > height {
		least := 0
		for i, p := range parts {
			if p.rank > parts[least].rank {
				least = i
			}
		}
		parts = slices.Delete(slices.Clone(parts), least, least+1)
	}
	if len(parts) == 1 && len(parts[0].lines) > height {
		if height <= 0 {
			return nil
		}
		l := parts[0].lines
		head := ansi.Truncate(l[0]+"…", width, "…")
		parts = []part{{append([]string{head}, l[len(l)-height+1:]...), parts[0].rank}}
	}
	return parts
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
