// Package tui is the interactive front end of lopper.
package tui

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/shinokamix/lopper/apps/lopper/internal/engine"
	"github.com/shinokamix/lopper/apps/lopper/internal/lopper"
)

type eventMsg struct {
	gen int
	ev  engine.Event
}

type rankMsg struct{ seq int }

const rankEvery = time.Second

type app struct {
	ctx      context.Context
	scan     func(context.Context) <-chan engine.Event
	remove   func(ctx context.Context, wt lopper.Worktree, force bool) error
	measure  func(context.Context, lopper.Worktree) *int64
	updates  Updates
	offer    *offer
	installs installs
	restart  bool
	gen      int
	ranking  int
	stop     context.CancelFunc
	events   <-chan engine.Event
	store    *store
	keys     keyMap
	theme    theme
	help     help.Model
	spin     spinner.Model
	list     list
	removal  *removal
	width    int
	height   int
}

// Engine is what the TUI needs of [engine.Engine].
type Engine interface {
	Scan(ctx context.Context, opts engine.Options) <-chan engine.Event
	Remove(ctx context.Context, wt lopper.Worktree, force bool) error
	Measure(ctx context.Context, wt lopper.Worktree) *int64
}

// Run offers any newer release, then scans and shows the results.
func Run(ctx context.Context, eng Engine, opts engine.Options, updates Updates) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	a := &app{
		ctx:     ctx,
		scan:    func(ctx context.Context) <-chan engine.Event { return eng.Scan(ctx, opts) },
		remove:  eng.Remove,
		measure: eng.Measure,
		updates: updates,
		store:   newStore(),
		keys:    defaultKeys(),
		theme:   newTheme(true),
		help:    help.New(),
		spin:    spinner.New(spinner.WithSpinner(spinner.Dot)),
		list:    newList(pathAliases()),
	}
	if updates.Latest != "" {
		a.offer = &offer{tag: updates.Latest}
	} else {
		a.startScan(ctx)
	}
	if err := a.run(cancel); err != nil || !a.restart {
		return err
	}
	return updates.Restart()
}

func (a *app) run(cancel context.CancelFunc, opts ...tea.ProgramOption) error {
	_, err := tea.NewProgram(a, append(opts, tea.WithContext(a.ctx))...).Run()
	cancel()
	a.installs.close()
	return err
}

func (a *app) Init() tea.Cmd {
	var next tea.Cmd
	if a.offer == nil {
		next = a.waitEvent()
	}
	return tea.Batch(next, tea.RequestBackgroundColor, a.spin.Tick)
}

func (a *app) startScanning() tea.Cmd {
	a.offer = nil
	a.startScan(a.ctx)
	return tea.Batch(a.waitEvent(), a.startRanking(), a.spin.Tick)
}

func (a *app) startScan(ctx context.Context) {
	if a.stop != nil {
		a.stop()
	}
	ctx, a.stop = context.WithCancel(ctx)
	a.gen++
	a.events = a.scan(ctx)
	a.store = newStore()
	bySize := a.list.bySize
	a.list = newList(a.list.aliases)
	a.list.bySize = bySize
}

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

func (a *app) startRanking() tea.Cmd {
	if !a.list.bySize || !a.store.scanning {
		return nil
	}
	a.ranking++
	return a.nextRank()
}

func (a *app) nextRank() tea.Cmd {
	seq := a.ranking
	return tea.Tick(rankEvery, func(time.Time) tea.Msg { return rankMsg{seq} })
}

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
			return a, nil
		}
		a.store.apply(msg.ev)
		if _, done := msg.ev.(engine.ScanDone); done {
			a.list.rank(a.store)
		}
		next := a.waitEvent()
		return a, next
	case rankMsg:
		if msg.seq != a.ranking || !a.list.bySize || !a.store.scanning {
			return a, nil
		}
		a.list.rank(a.store)
		next := a.nextRank()
		return a, next
	case tea.PasteMsg:
		if a.offer == nil && a.removal == nil && a.list.searching {
			a.list.search(a.list.query + strings.ReplaceAll(msg.Content, "\n", " "))
		}
	case removedMsg:
		cmd := a.removed(msg)
		return a, cmd
	case installedMsg:
		a.offer.phase, a.offer.err = installed, msg.err
		if msg.err != nil {
			a.offer.phase = offered
		}
	case frameMsg:
		if rm := msg.rm; rm == a.removal && rm.frame < frames {
			rm.frame++
			return a, rm.nextFrame()
		}
	case spinner.TickMsg:
		busy := (a.offer != nil && a.offer.phase == installing) ||
			(a.offer == nil && a.store.scanning) || (a.removal != nil && a.removal.phase == removing)
		if !busy {
			return a, nil
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
		if a.offer != nil {
			cmd := a.offerKey(msg)
			return a, cmd
		}
		if a.removal != nil {
			cmd := a.removalKey(msg)
			return a, cmd
		}
		if a.list.allKeys {
			cmd := a.keysKey(msg)
			return a, cmd
		}
		if a.list.searching {
			if msg.String() == "ctrl+c" {
				return a, tea.Quit
			}
			a.list.searchKey(msg, a.keys, a.store)
			return a, nil
		}
		switch {
		case key.Matches(msg, a.keys.quit):
			return a, tea.Quit
		case key.Matches(msg, a.keys.search):
			a.list.searching = true
		case key.Matches(msg, a.keys.clear) && a.list.query != "":
			a.list.search("")
		case key.Matches(msg, a.keys.sort):
			a.list.sort(a.store)
			cmd := a.startRanking()
			return a, cmd
		case key.Matches(msg, a.keys.help):
			a.list.allKeys = true
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

func (a *app) keysKey(msg tea.KeyPressMsg) tea.Cmd {
	switch {
	case key.Matches(msg, a.keys.quit):
		return tea.Quit
	case key.Matches(msg, a.keys.help), key.Matches(msg, a.keys.back):
		a.list.allKeys, a.list.keysOffset = false, 0
	case key.Matches(msg, a.keys.up):
		a.list.keysOffset--
	case key.Matches(msg, a.keys.down):
		a.list.keysOffset++
	}
	return nil
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
			rm.offset--
		case key.Matches(msg, a.keys.down):
			rm.offset++
		}
	case removing:
		// Killing git midway can leave a worktree half deleted.
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
			return a.startScanning()
		case key.Matches(msg, a.keys.up):
			rm.offset--
		case key.Matches(msg, a.keys.down):
			rm.offset++
		}
	}
	return nil
}

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
		rm.phase, rm.offset = finished, 0
		return rm.nextFrame()
	}
	return a.removeNext()
}

const (
	chromeLines = 6
	margin      = 2
)

func (a *app) View() tea.View {
	w := max(a.width-2*margin, 1)
	spin := strings.TrimSpace(a.spin.View())
	var screen string
	switch {
	case a.offer != nil:
		screen = a.offer.view(a.theme, a.help, a.keys, a.updates, spin, w, max(a.height-1, 1))
	case a.removal != nil && a.removal.phase == finished:
		screen = a.removal.summary(a.theme, a.help, a.keys, w, max(a.height-1, 1))
	case a.removal != nil:
		screen = a.removal.view(a.theme, a.help, a.keys, spin, w, max(a.height-1, 1))
	default:
		height := max(a.height-chromeLines, 1)
		var body string
		if a.list.allKeys {
			body = a.list.keysView(a.help, a.keys, w, height)
		} else {
			body = a.list.view(a.theme, a.store, spin, w, height)
		}
		screen = a.list.header(a.theme, a.store, a.spin.View(), w) + "\n\n" + body + "\n\n" +
			a.list.footer(a.theme, a.store, a.help, a.keys, spin, w)
	}
	lines := strings.Split(screen, "\n")
	for i, l := range lines {
		lines[i] = strings.Repeat(" ", margin) + ansi.Truncate(l, w, "…")
	}
	v := tea.NewView("\n" + strings.Join(lines, "\n"))
	v.AltScreen = true
	return v
}

func pathAliases() []alias {
	var out []alias
	add := func(dir, name string) {
		if dir == "" {
			return
		}
		out = append(out, alias{dir, name})
		// On macOS /tmp is a symlink to /private/tmp.
		if resolved, err := filepath.EvalSymlinks(dir); err == nil && resolved != dir {
			out = append(out, alias{resolved, name})
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
