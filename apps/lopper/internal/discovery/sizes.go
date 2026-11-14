package discovery

import (
	"context"
	"errors"
	"sync"

	"github.com/shinokamix/lopper/apps/lopper/internal/lopper"
)

var errNotMeasured = errors.New("the scan ended before measuring it")

// Sizes holds the size of every worktree one Scan emits. The walk that
// finds a worktree measures it, so its files are read once.
type Sizes struct {
	mu      sync.Mutex
	entries map[string]*measured
	wanted  []string
	ended   bool
}

type measured struct {
	ready chan struct{}
	size  int64
	err   error
}

// NewSizes returns Sizes for one Scan.
func NewSizes() *Sizes {
	return &Sizes{entries: map[string]*measured{}}
}

// Wait returns the size in bytes of the regular files below path, symlinks
// not followed, once the Scan has measured it. Unreadable entries below
// path are left out. A gone worktree takes no space. It returns an error if
// path could not be read, or if the Scan or ctx ended first.
func (s *Sizes) Wait(ctx context.Context, path string) (int64, error) {
	m := s.entry(realPath(path))
	select {
	case <-m.ready:
		return m.size, m.err
	case <-ctx.Done():
		return 0, ctx.Err()
	}
}

func (s *Sizes) entry(key string) *measured {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.entryLocked(key)
}

func (s *Sizes) entryLocked(key string) *measured {
	m, ok := s.entries[key]
	if !ok {
		m = &measured{ready: make(chan struct{})}
		s.entries[key] = m
		if s.ended {
			m.err = errNotMeasured
			close(m.ready)
		}
	}
	return m
}

func (s *Sizes) track(emit func(lopper.Worktree)) func(lopper.Worktree) {
	return func(wt lopper.Worktree) {
		if wt.State == lopper.StateGone {
			s.publish(wt.Path, 0, nil)
		} else {
			s.mu.Lock()
			s.wanted = append(s.wanted, wt.Path)
			s.mu.Unlock()
		}
		emit(wt)
	}
}

func (s *Sizes) unmeasured() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var dirs []string
	for _, dir := range s.wanted {
		if !s.entryLocked(realPath(dir)).done() {
			dirs = append(dirs, dir)
		}
	}
	return dirs
}

func (s *Sizes) publish(dir string, size int64, err error) {
	key := realPath(dir)
	s.mu.Lock()
	defer s.mu.Unlock()
	m := s.entryLocked(key)
	if !m.done() {
		m.size, m.err = size, err
		close(m.ready)
	}
}

func (m *measured) done() bool {
	select {
	case <-m.ready:
		return true
	default:
		return false
	}
}

func (s *Sizes) end() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ended {
		return
	}
	s.ended = true
	for _, m := range s.entries {
		if !m.done() {
			m.err = errNotMeasured
			close(m.ready)
		}
	}
}
