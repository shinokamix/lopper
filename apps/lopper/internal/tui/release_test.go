package tui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/shinokamix/lopper/apps/lopper/internal/engine"
)

// updatingApp is the app as Run starts it with release v0.2.0 to offer.
// scans counts the scans started, put off the releases postponed or
// skipped, as "postpone <tag>" or "skip <tag>".
func updatingApp(t *testing.T, install func(context.Context, string) error) (a *app, scans *int, putOff *[]string) {
	a = testApp()
	a.ctx = t.Context()
	scans, putOff = new(int), new([]string)
	a.scan = func(context.Context) <-chan engine.Event {
		*scans++
		ch := make(chan engine.Event)
		close(ch)
		return ch
	}
	a.updates = Updates{
		Current: "v0.1.0", Latest: "v0.2.0", Repo: "https://github.com/shinokamix/lopper",
		Install:  install,
		Postpone: func(tag string) { *putOff = append(*putOff, "postpone "+tag) },
		Skip:     func(tag string) { *putOff = append(*putOff, "skip "+tag) },
	}
	a.offer = &offer{tag: "v0.2.0"}
	a.Update(tea.WindowSizeMsg{Width: 120, Height: 20})
	return a, scans, putOff
}

// A newer release is offered on its own screen before anything is
// scanned. Esc puts it off and s skips it. Either one scans with the
// running version and is remembered.
func TestNewerReleaseIsOfferedBeforeTheScan(t *testing.T) {
	for _, tc := range []struct {
		key  rune
		want string
	}{
		{tea.KeyEscape, "postpone v0.2.0"},
		{'s', "skip v0.2.0"},
	} {
		t.Run(tc.want, func(t *testing.T) {
			a, scans, putOff := updatingApp(t, nil)
			screen := view(a)
			for _, want := range []string{"lopper v0.1.0 → v0.2.0", "https://github.com/shinokamix/lopper/releases/tag/v0.2.0", "enter update", "esc not now", "s skip this version"} {
				if !strings.Contains(screen, want) {
					t.Errorf("update screen lacks %q:\n%s", want, screen)
				}
			}
			if *scans != 0 {
				t.Errorf("scan started while the update is offered")
			}

			settle(a, press(a, tc.key))
			if screen := view(a); *scans != 1 || strings.Contains(screen, "v0.2.0") {
				t.Errorf("did not go on to scan (%d scans):\n%s", *scans, screen)
			}
			if !slices.Equal(*putOff, []string{tc.want}) {
				t.Errorf("remembered %q, want %q", *putOff, tc.want)
			}
		})
	}
}

// Enter installs the offered release. A failure says why, and enter tries
// again. Once installed, enter restarts into it rather than scanning.
func TestOfferedReleaseIsInstalledWithEnter(t *testing.T) {
	var installed []string
	a, scans, _ := updatingApp(t, func(_ context.Context, tag string) error {
		installed = append(installed, tag)
		if len(installed) == 1 {
			return errors.New("download: connection reset")
		}
		return nil
	})

	settle(a, press(a, tea.KeyEnter))
	if screen := view(a); !strings.Contains(screen, "update failed: download: connection reset") || !strings.Contains(screen, "enter update") {
		t.Errorf("failed install is not shown with a way to retry:\n%s", screen)
	}
	settle(a, press(a, tea.KeyEnter))
	if len(installed) != 2 || installed[1] != "v0.2.0" {
		t.Fatalf("installed %q, want v0.2.0 again after the failure", installed)
	}
	if screen := view(a); !strings.Contains(screen, "Updated to lopper v0.2.0") || !strings.Contains(screen, "enter restart") || strings.Contains(screen, "failed") {
		t.Errorf("finished install is not shown:\n%s", screen)
	}
	if _, isQuit := press(a, tea.KeyEnter)().(tea.QuitMsg); !isQuit || !a.restart {
		t.Errorf("enter after the update did not quit to restart")
	}
	if *scans != 0 {
		t.Errorf("scan started though lopper restarts")
	}
}

// Where lopper may not write, installing again would fail again. Only
// putting it off is offered, and the error says how to update instead.
func TestInstallWithoutPermissionIsNotRetried(t *testing.T) {
	var tries int
	a, _, _ := updatingApp(t, func(context.Context, string) error {
		tries++
		return fmt.Errorf("cannot write to /usr/local/bin: %w; run sudo lopper update", fs.ErrPermission)
	})
	settle(a, press(a, tea.KeyEnter))
	settle(a, press(a, tea.KeyEnter))
	screen := view(a)
	if tries != 1 || strings.Contains(screen, "enter update") || !strings.Contains(screen, "esc not now") {
		t.Errorf("install without permission is offered again (%d tries):\n%s", tries, screen)
	}
	if !strings.Contains(screen, "run sudo lopper update") {
		t.Errorf("the way to update is not shown:\n%s", screen)
	}
}

// brokenTerminal fails every read, as a terminal that went away does.
type brokenTerminal struct{}

func (brokenTerminal) Read([]byte) (int, error) { return 0, errors.New("input/output error") }

// However the TUI ends while the release installs, by q or by its
// terminal failing, it does so at once and stops the download, which
// could otherwise hold lopper open for minutes.
func TestEndingWhileInstallingStopsIt(t *testing.T) {
	typed := func(keys string) io.Reader {
		r, w := io.Pipe() // left open, so only the keys end the TUI
		go func() { _, _ = w.Write([]byte(keys)) }()
		return r
	}
	for _, tc := range []struct {
		name    string
		input   io.Reader
		failure bool
	}{
		{"q", typed("q"), false},
		{"terminal error", brokenTerminal{}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			started := make(chan struct{})
			a, _, _ := updatingApp(t, func(ctx context.Context, _ string) error {
				close(started)
				<-ctx.Done() // a download that never ends
				return ctx.Err()
			})
			var cancel context.CancelFunc
			a.ctx, cancel = context.WithCancel(a.ctx)
			defer cancel()
			go a.install()()
			<-started
			if screen := view(a); !strings.Contains(screen, "installing…") || !strings.Contains(screen, "q quit") {
				t.Errorf("installing screen does not offer q:\n%s", screen)
			}
			done := make(chan error)
			go func() { done <- a.run(cancel, tea.WithInput(tc.input), tea.WithOutput(io.Discard)) }()
			select {
			case err := <-done:
				if (err != nil) != tc.failure || a.restart {
					t.Errorf("TUI ended with %v, restart %v", err, a.restart)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("the install went on after the TUI ended")
			}
		})
	}
}

// On a small screen, every way out of the update screen stays visible,
// and so does what to do about an install that failed.
func TestUpdateScreenFitsSmallScreen(t *testing.T) {
	a, _, _ := updatingApp(t, func(context.Context, string) error {
		return fmt.Errorf("cannot write to /usr/local/bin: open /usr/local/bin/.lopper-update-3141592: %w; run sudo lopper update", fs.ErrPermission)
	})
	a.Update(tea.WindowSizeMsg{Width: 34, Height: 8})
	lines := plainLines(a)
	for _, want := range []string{"enter update", "esc not now", "s skip this version", "q quit"} {
		if !strings.Contains(view(a), want) {
			t.Errorf("small update screen lacks %q:\n%s", want, view(a))
		}
	}
	for _, l := range lines {
		if w := ansi.StringWidth(l); w > 34 {
			t.Errorf("line is %d cells wide on a 34-cell screen: %q", w, l)
		}
	}
	if len(lines) > 8 {
		t.Errorf("%d lines on an 8-line screen:\n%s", len(lines), view(a))
	}

	settle(a, press(a, tea.KeyEnter))
	text := strings.Join(strings.Fields(view(a)), " ")
	for _, want := range []string{"update failed", "run sudo lopper update", "esc not now", "s skip this version", "q quit"} {
		if !strings.Contains(text, want) {
			t.Errorf("after the error, small update screen lacks %q:\n%s", want, view(a))
		}
	}
	if n := len(plainLines(a)); n > 8 {
		t.Errorf("%d lines on an 8-line screen:\n%s", n, view(a))
	}
	// One line is left for the error. It keeps how to update, since
	// installing again is not offered.
	a.Update(tea.WindowSizeMsg{Width: 34, Height: 6})
	for _, want := range []string{"run sudo lopper update", "esc not now", "s skip this version", "q quit"} {
		if !strings.Contains(view(a), want) {
			t.Errorf("after the error, a 34×6 screen lacks %q:\n%s", want, view(a))
		}
	}
	for _, l := range plainLines(a) {
		if w := ansi.StringWidth(l); w > 34 {
			t.Errorf("line is %d cells wide on a 34-cell screen: %q", w, l)
		}
	}
	if n := len(plainLines(a)); n > 6 {
		t.Errorf("%d lines on a 6-line screen:\n%s", n, view(a))
	}
}

// Quitting may drop the install command before it runs. Then no install
// starts, and lopper exits rather than wait for it.
func TestInstallDroppedByQuitNeitherRunsNorHoldsExit(t *testing.T) {
	var calls int
	a, _, _ := updatingApp(t, func(context.Context, string) error { calls++; return nil })
	cmd := a.install()
	exited := make(chan struct{})
	go func() { a.installs.close(); close(exited) }()
	select {
	case <-exited:
	case <-time.After(5 * time.Second):
		t.Fatal("exit waits for an install that never started")
	}
	if msg := cmd(); msg != nil || calls != 0 {
		t.Errorf("install ran after the TUI quit: %v, %d calls", msg, calls)
	}
}

// lopper waits on exit for an install that started, since on Windows
// exiting midway could leave no lopper binary.
func TestExitWaitsForRunningInstall(t *testing.T) {
	started, finish := make(chan struct{}), make(chan struct{})
	a, _, _ := updatingApp(t, func(context.Context, string) error {
		close(started)
		<-finish
		return nil
	})
	go a.install()()
	<-started
	exited := make(chan struct{})
	go func() { a.installs.close(); close(exited) }()
	select {
	case <-exited:
		t.Fatal("exit did not wait for the running install")
	case <-time.After(50 * time.Millisecond):
	}
	close(finish)
	<-exited
}
