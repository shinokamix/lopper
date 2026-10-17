package engine

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"testing/synctest"

	"github.com/shinokamix/lopper/apps/lopper/internal/lopper"
	"github.com/shinokamix/lopper/apps/lopper/internal/verdict"
)

type fakeGit struct {
	n       int
	block   bool
	started chan<- struct{}
}

func (f fakeGit) RunRaw(ctx context.Context, dir, _ string, args ...string) (string, error) {
	return f.Run(ctx, dir, args...)
}

func (f fakeGit) Run(ctx context.Context, dir string, args ...string) (string, error) {
	if len(args) > 1 && args[0] == "worktree" && args[1] == "list" {
		sep := "\n"
		if slices.Contains(args, "-z") {
			sep = "\x00"
		}
		var b strings.Builder
		record := func(path, branch string) {
			b.WriteString("worktree " + path + sep + "HEAD 1" + sep + "branch refs/heads/" + branch + sep + sep)
		}
		record(dir, "main")
		for i := range f.n {
			record(filepath.Join(dir, "wt", strconv.Itoa(i)), "b"+strconv.Itoa(i))
		}
		return b.String(), nil
	}
	if f.block && filepath.Base(filepath.Dir(dir)) == "wt" {
		select {
		case f.started <- struct{}{}:
		default:
		}
		<-ctx.Done()
		return "", ctx.Err()
	}
	return "", errors.New("fake git: unsupported")
}

func newRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "repo", ".git", "worktrees", "x"), 0o755); err != nil {
		t.Fatal(err)
	}
	return root
}

type checker struct {
	t      *testing.T
	known  map[lopper.ID]bool
	final  int
	done   bool
	doneEv ScanDone
}

func newChecker(t *testing.T) *checker {
	return &checker{t: t, known: map[lopper.ID]bool{}}
}

func (c *checker) see(ev Event) {
	c.t.Helper()
	if c.done {
		c.t.Fatalf("event %T after ScanDone", ev)
	}
	switch ev := ev.(type) {
	case WorktreeFound:
		if c.known[ev.Worktree.ID] {
			c.t.Errorf("WorktreeFound twice for %s", ev.Worktree.ID)
		}
		c.known[ev.Worktree.ID] = true
	case FactsUpdated:
		if !c.known[ev.ID] {
			c.t.Fatalf("FactsUpdated for %s before its WorktreeFound", ev.ID)
		}
		if ev.Final {
			c.final++
		}
	case ScanDone:
		c.done, c.doneEv = true, ev
	}
}

func TestScanEventOrder(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const n = 300
		c := newChecker(t)
		for ev := range New().withGit(fakeGit{n: n}).Scan(t.Context(), Options{Roots: []string{newRepo(t)}}) {
			c.see(ev)
		}
		if !c.done || c.doneEv.Err != nil {
			t.Fatalf("scan ended with done=%v err=%v, want ScanDone without error", c.done, c.doneEv.Err)
		}
		if len(c.known) != n || c.final != n {
			t.Errorf("got %d worktrees and %d final updates, want %d of each", len(c.known), c.final, n)
		}
	})
}

func TestScanSendsVerdictWithNotes(t *testing.T) {
	_, path := gitRepo(t)
	git(t, path, "commit", "-q", "--allow-empty", "-m", "work")

	var wt lopper.Worktree
	var updates, final int
	for ev := range New().Scan(t.Context(), Options{Roots: []string{path}}) {
		switch ev := ev.(type) {
		case WorktreeFound:
			wt = ev.Worktree
		case FactsUpdated:
			updates++
			if want := verdict.Of(wt, ev.Facts); !reflect.DeepEqual(ev.Verdict, want) {
				t.Errorf("update %d has verdict %+v, want %+v", updates, ev.Verdict, want)
			}
			if ev.Final {
				final++
				if ev.Verdict.Safe || len(ev.Verdict.Notes) == 0 {
					t.Errorf("final verdict %+v, want unsafe with notes for an unpushed commit", ev.Verdict)
				}
			}
		case ScanDone:
			if ev.Err != nil {
				t.Fatal(ev.Err)
			}
		}
	}
	if updates != 2 || final != 1 {
		t.Errorf("got %d updates and %d final, want 2 and 1", updates, final)
	}
}

func TestScanReportsRootError(t *testing.T) {
	c := newChecker(t)
	missing := filepath.Join(t.TempDir(), "missing")
	for ev := range New().withGit(fakeGit{}).Scan(t.Context(), Options{Roots: []string{missing}}) {
		c.see(ev)
	}
	if !c.done || c.doneEv.Err == nil {
		t.Fatalf("scan of %s ended with done=%v err=%v, want an error", missing, c.done, c.doneEv.Err)
	}
}

func TestScanCancelMidScan(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		started := make(chan struct{}, 1)
		c := newChecker(t)
		events := New().withGit(fakeGit{n: 500, block: true, started: started}).Scan(ctx, Options{Roots: []string{newRepo(t)}})
		c.see(<-events)
		<-started
		cancel()
		for ev := range events {
			c.see(ev)
		}
	})
}

func TestScanAbandonedConsumer(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		events := New().withGit(fakeGit{n: 500, block: true}).Scan(ctx, Options{Roots: []string{newRepo(t)}})
		<-events
		cancel()
		synctest.Wait()
	})
}

func TestBudget(t *testing.T) {
	for _, n := range []int{0, 1, 2, 3, 8, 64} {
		l, w := budget(n)
		if l < 1 || w < 1 || (n >= 2 && l+w > n) {
			t.Errorf("budget(%d) = %d listers, %d workers", n, l, w)
		}
	}
}

func (e *Engine) withGit(g fakeGit) *Engine {
	e.git = g
	return e
}
