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

// Updates offers a newer release before the scan. The zero value offers none.
type Updates struct {
	Current string
	// Latest is empty when no newer release is out.
	Latest string
	// Repo is the URL that release notes live under at releases/tag/<tag>.
	Repo string
	// Install replaces the running binary with release tag.
	Install func(ctx context.Context, tag string) error
	// Postpone stops offering release tag for a while, and Skip for good.
	Postpone, Skip func(tag string)
	// Restart runs the installed release in place of this process. It
	// returns only if it fails.
	Restart func() error
}

type installedMsg struct{ err error }

type offer struct {
	tag   string
	phase offerPhase
	err   error
}

type offerPhase int

const (
	offered offerPhase = iota
	installing
	installed
)

func (o *offer) retryable() bool {
	return !errors.Is(o.err, fs.ErrPermission)
}

// installs keeps lopper alive through an install. On Windows, Install renames
// lopper away before it renames the new binary in, and exiting between the two
// leaves neither.
type installs struct {
	mu      sync.Mutex
	closed  bool
	running sync.WaitGroup
}

// start runs inside the command because Bubble Tea may drop a command it has
// not run when the program quits.
func (s *installs) start() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return false
	}
	s.running.Add(1)
	return true
}

func (s *installs) close() {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
	s.running.Wait()
}

func (a *app) install() tea.Cmd {
	install, ctx, tag, gate := a.updates.Install, a.ctx, a.offer.tag, &a.installs
	a.offer.phase, a.offer.err = installing, nil
	return func() tea.Msg {
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

func (o *offer) view(t theme, h help.Model, k keyMap, u Updates, spin string, width, height int) string {
	w := max(width-1, 1)
	wrap := func(s string) []string { return strings.Split(ansi.Wrap(s, w, ""), "\n") }
	title := wrap(t.title.Render("Update available: lopper " + u.Current + " → " + o.tag))
	var parts []part
	var keys bindings
	switch o.phase {
	case offered:
		parts = append(parts, part{title, 2})
		if o.err != nil {
			parts = append(parts, part{wrap(t.failure.Render("update failed: " + o.err.Error())), 1})
		}
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
		room -= len(keyHelp) + 1
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

type part struct {
	lines []string
	rank  int
}

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
		l, rank := parts[0].lines, parts[0].rank
		switch {
		case height <= 0:
			return nil
		case height == 1:
			end := strings.Join(l, " ")
			return []part{{[]string{ansi.TruncateLeft(end, ansi.StringWidth(end)-width+1, "…")}, rank}}
		}
		head := ansi.Truncate(l[0]+"…", width, "…")
		parts = []part{{append([]string{head}, l[len(l)-height+1:]...), rank}}
	}
	return parts
}

func helpLines(h help.Model, keys bindings, width int) []string {
	if len(keys) == 0 {
		return nil
	}
	h = untruncated(h)
	if line := h.ShortHelpView(keys); ansi.StringWidth(line) <= width {
		return []string{line}
	}
	lines := make([]string, len(keys))
	for i, b := range keys {
		lines[i] = h.ShortHelpView([]key.Binding{b})
	}
	return lines
}
