package discovery

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"

	"github.com/charlievieth/fastwalk"
)

type walker struct {
	ctx      context.Context
	sizes    *Sizes
	visited  *fastwalk.EntryFilter
	everyDir bool
	dotGit   func(path string, typ fs.FileMode)
	report   func(gitDir string, at place)

	mu      sync.Mutex
	more    *sync.Cond
	queue   []task
	pending int
	links   []string
}

func newWalker(ctx context.Context, sizes *Sizes, dotGit func(path string, typ fs.FileMode), report func(gitDir string, at place)) *walker {
	w := &walker{
		ctx:     ctx,
		sizes:   sizes,
		visited: fastwalk.NewEntryFilter(),
		// Only links and roots lead to a directory twice. On Unix the check
		// is a stat, so every directory gets one and a linked directory the
		// walk reached first is not walked again. On Windows it opens the
		// directory, so only links and roots are checked.
		everyDir: runtime.GOOS != "windows",
		dotGit:   dotGit,
		report:   report,
	}
	w.more = sync.NewCond(&w.mu)
	return w
}

type task struct {
	dir    string
	owner  *owner
	search bool
}

type owner struct {
	dir    string
	parent *owner
	size   atomic.Int64
	tasks  atomic.Int64
	err    error
}

func (w *walker) search(root string) error {
	if w.claim(root) {
		err := w.do(task{dir: root, search: true})
		w.run()
		if err != nil {
			return fmt.Errorf("scan %s: %w", root, err)
		}
	}
	for len(w.links) > 0 && w.ctx.Err() == nil {
		var tasks []task
		for _, link := range w.links {
			if w.claim(link) {
				tasks = append(tasks, task{dir: link, search: true})
			}
		}
		w.links = nil
		w.push(tasks)
		w.run()
	}
	return w.ctx.Err()
}

func (w *walker) measure(dirs []string) {
	tasks := make([]task, len(dirs))
	for i, dir := range dirs {
		tasks[i] = task{dir: dir, owner: &owner{dir: dir}}
	}
	w.push(tasks)
	w.run()
}

func (w *walker) claim(dir string) bool {
	info, err := os.Stat(dir)
	if err != nil {
		return false
	}
	d := fs.FileInfoToDirEntry(info)
	return !w.visited.Entry(dir, d) && !w.gitEntry(dir, d.Name(), d.Type())
}

func (w *walker) run() {
	var wg sync.WaitGroup
	for range fastwalk.DefaultNumWorkers() {
		wg.Go(w.work)
	}
	wg.Wait()
}

func (w *walker) work() {
	w.mu.Lock()
	defer w.mu.Unlock()
	for {
		for len(w.queue) == 0 {
			if w.pending == 0 {
				return
			}
			w.more.Wait()
		}
		t := w.queue[len(w.queue)-1]
		w.queue = w.queue[:len(w.queue)-1]
		w.mu.Unlock()
		_ = w.do(t)
		w.mu.Lock()
		w.pending--
		if w.pending == 0 {
			w.more.Broadcast()
		}
	}
}

func (w *walker) push(tasks []task) {
	for _, t := range tasks {
		if t.owner != nil {
			t.owner.tasks.Add(1)
		}
	}
	w.mu.Lock()
	w.queue = append(w.queue, tasks...)
	w.pending += len(tasks)
	w.mu.Unlock()
	for range tasks {
		w.more.Signal()
	}
}

func (w *walker) do(t task) error {
	o := t.owner
	defer func() { w.finish(o) }()
	if w.ctx.Err() != nil {
		return nil
	}
	entries, err := readDir(t.dir)
	switch {
	case err != nil && o != nil && o.dir == t.dir:
		o.err = err
	case err == nil && t.search && w.sizes != nil && hasGitFile(entries):
		o = &owner{dir: t.dir, parent: o}
		o.tasks.Store(1)
	}
	var (
		size  int64
		tasks []task
	)
	for _, d := range entries {
		typ := d.Type()
		if typ&fs.ModeSymlink == 0 && !typ.IsDir() {
			if o != nil {
				size += fileSize(d)
			}
			if t.search && d.Name() == ".git" {
				w.dotGit(filepath.Join(t.dir, ".git"), typ)
			}
			continue
		}
		path := filepath.Join(t.dir, d.Name())
		switch {
		case typ&fs.ModeSymlink != 0:
			if t.search {
				w.link(path, d)
			}
		case !t.search:
			tasks = append(tasks, task{path, o, false})
		case w.everyDir && w.visited.Entry(path, d) || w.gitEntry(path, d.Name(), typ):
			if o != nil {
				tasks = append(tasks, task{path, o, false})
			}
		default:
			tasks = append(tasks, task{path, o, true})
		}
	}
	if o != nil {
		o.size.Add(size)
	}
	w.push(tasks)
	return err
}

func (w *walker) finish(o *owner) {
	for o != nil && o.tasks.Add(-1) == 0 {
		size := o.size.Load()
		if w.ctx.Err() == nil {
			w.sizes.publish(o.dir, size, o.err)
		}
		o = o.parent
		if o != nil {
			o.size.Add(size)
		}
	}
}

func (w *walker) link(path string, d fs.DirEntry) {
	info, err := os.Stat(path)
	if err != nil {
		return
	}
	typ := info.Mode().Type()
	switch {
	case !typ.IsDir():
		w.gitEntry(path, d.Name(), typ)
	case isGitEntry(path, d.Name(), typ):
		if !w.visited.Entry(path, d) {
			w.gitEntry(path, d.Name(), typ)
		}
	default:
		w.mu.Lock()
		w.links = append(w.links, path)
		w.mu.Unlock()
	}
}

func readDir(dir string) ([]fs.DirEntry, error) {
	f, err := os.Open(dir)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return f.ReadDir(-1)
}

func hasGitFile(entries []fs.DirEntry) bool {
	for _, d := range entries {
		if d.Name() == ".git" && d.Type().IsRegular() {
			return true
		}
	}
	return false
}

func isGitEntry(path, name string, typ fs.FileMode) bool {
	return name == ".git" || name == "worktrees" && typ.IsDir() && isGitDir(filepath.Dir(path))
}

func (w *walker) gitEntry(path, name string, typ fs.FileMode) bool {
	switch {
	case !isGitEntry(path, name, typ):
		return false
	case name == ".git":
		w.dotGit(path, typ)
	default:
		w.report(filepath.Dir(path), place{filepath.Dir(path), true})
	}
	return true
}

func fileSize(d fs.DirEntry) int64 {
	if !d.Type().IsRegular() {
		return 0
	}
	if info, err := d.Info(); err == nil {
		return info.Size()
	}
	return 0
}
