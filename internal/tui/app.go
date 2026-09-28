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
	"github.com/shinokamix/lopper/internal/lopper"
)

// eventMsg carries an event of scan number gen: events of a scan
// replaced by a new one are dropped.
type eventMsg struct {
	gen int
	ev  engine.Event
}

type app struct {
	ctx     context.Context
	scan    func(context.Context) <-chan engine.Event
	remove  func(ctx context.Context, wt lopper.Worktree, force bool) error
	measure func(context.Context, lopper.Worktree) *int64
	gen     int                // counts scans
	stop    context.CancelFunc // stops the current scan
	events  <-chan engine.Event
	store   *store
	keys    keyMap
	theme   theme
	help    help.Model
	spin    spinner.Model
	list    list
	// removal is the removal screen, shown over the list while not nil.
	removal *removal
	width   int
	height  int
}

// Run starts the TUI and a scan feeding it.
func Run(ctx context.Context, eng *engine.Engine, opts engine.Options) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	a := &app{
		ctx:     ctx,
		scan:    func(ctx context.Context) <-chan engine.Event { return eng.Scan(ctx, opts) },
		remove:  eng.Remove,
		measure: eng.Measure,
		keys:    defaultKeys(),
		theme:   newTheme(true),
		help:    help.New(),
		spin:    spinner.New(spinner.WithSpinner(spinner.Dot)),
		list:    newList(pathAliases()),
	}
	a.startScan(ctx)
	_, err := tea.NewProgram(a, tea.WithContext(ctx)).Run()
	return err
}

func (a *app) Init() tea.Cmd {
	return tea.Batch(a.waitEvent(), tea.RequestBackgroundColor, a.spin.Tick)
}

// startScan starts a scan into an empty list, stopping the one before.
func (a *app) startScan(ctx context.Context) {
	if a.stop != nil {
		a.stop()
	}
	ctx, a.stop = context.WithCancel(ctx)
	a.gen++
	a.events = a.scan(ctx)
	a.store = newStore()
	a.list = newList(a.list.aliases)
}

// waitEvent bridges the engine channel into Bubble Tea messages.
// TODO: coalesce bursts of events into one message per frame.
func (a *app) waitEvent() tea.Cmd {
	events, gen := a.events, a.gen
	return func() tea.Msg {
		ev, ok := <-events
		if !ok {
			return nil
		}
		return eventMsg{gen, ev}
	}
}

// removeNext removes the next item of the removal screen. One the scan
// has not measured yet is measured first, to tell the space freed.
func (a *app) removeNext() tea.Cmd {
	ctx, remove, measure, i := a.ctx, a.remove, a.measure, a.removal.next
	it := a.removal.items[i]
	wt, force, size := it.row.worktree, it.force(), it.row.facts.SizeBytes
	return func() tea.Msg {
		if size == nil {
			size = measure(ctx, wt)
		}
		return removedMsg{i: i, size: size, err: remove(ctx, wt, force)}
	}
}

func (a *app) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case eventMsg:
		if msg.gen != a.gen {
			return a, nil // from a scan replaced by a new one
		}
		a.store.apply(msg.ev)
		next := a.waitEvent()
		return a, next
	case removedMsg:
		cmd := a.removed(msg)
		return a, cmd
	case frameMsg:
		if rm := msg.rm; rm == a.removal && rm.frame < frames {
			rm.frame++
			return a, rm.nextFrame()
		}
	case spinner.TickMsg:
		if !a.store.scanning && (a.removal == nil || a.removal.phase != removing) {
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
		if a.removal != nil {
			cmd := a.removalKey(msg)
			return a, cmd
		}
		switch {
		case key.Matches(msg, a.keys.quit):
			return a, tea.Quit
		case key.Matches(msg, a.keys.remove):
			if rows := a.list.targets(a.store); len(rows) > 0 {
				a.removal = newRemoval(rows)
			}
		default:
			a.list.update(msg, a.keys, a.store)
		}
	}
	return a, nil
}

func (a *app) removalKey(msg tea.KeyPressMsg) tea.Cmd {
	rm := a.removal
	switch rm.phase {
	case confirming:
		switch {
		case key.Matches(msg, a.keys.quit):
			return tea.Quit
		case key.Matches(msg, a.keys.back):
			a.removal = nil
		case key.Matches(msg, a.keys.confirm) && rm.checked():
			rm.confirm()
			return tea.Batch(a.removeNext(), a.spin.Tick)
		case key.Matches(msg, a.keys.up):
			rm.offset-- // View keeps it in range
		case key.Matches(msg, a.keys.down):
			rm.offset++
		}
	case removing:
		// Stopping git halfway through a removal could leave the worktree
		// half deleted: quitting waits for the one being removed.
		if key.Matches(msg, a.keys.stop) {
			rm.quitting = true
		}
	case finished:
		switch {
		case key.Matches(msg, a.keys.quit):
			return tea.Quit
		case key.Matches(msg, a.keys.back):
			a.removal = nil
		case key.Matches(msg, a.keys.rescan):
			a.removal = nil
			a.startScan(a.ctx)
			return tea.Batch(a.waitEvent(), a.spin.Tick)
		case key.Matches(msg, a.keys.up):
			rm.offset-- // View keeps it in range
		case key.Matches(msg, a.keys.down):
			rm.offset++
		}
	}
	return nil
}

// removed records how removing an item went, and goes on to the next.
func (a *app) removed(msg removedMsg) tea.Cmd {
	rm := a.removal
	it := rm.items[msg.i]
	it.done, it.err, it.row.facts.SizeBytes = true, msg.err, msg.size
	if it.removed() {
		a.list.drop(a.store, it.row.worktree.ID)
	}
	rm.next++
	switch {
	case rm.quitting:
		return tea.Quit
	case rm.next == len(rm.items):
		rm.phase, rm.offset = finished, 0 // the summary scrolls on its own
		return rm.nextFrame()
	}
	return a.removeNext()
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
	spin := strings.TrimSpace(a.spin.View())
	var screen string
	switch {
	case a.removal != nil && a.removal.phase == finished:
		screen = a.removal.summary(a.theme, a.help, a.keys, w, max(a.height-1, 1))
	case a.removal != nil:
		screen = a.removal.view(a.theme, a.help, a.keys, spin, w, max(a.height-1, 1))
	default:
		body := a.list.view(a.theme, a.store, spin, w, max(a.height-chromeLines, 1))
		screen = header(a.theme, a.store, a.spin.View(), w) + "\n\n" +
			body + "\n\n" + a.list.footer(a.theme, a.store, a.help, a.keys, spin, w)
	}
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
