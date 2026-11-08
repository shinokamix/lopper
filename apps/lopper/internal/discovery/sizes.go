package discovery

import (
	"context"
	"sync"
)

// Sizes receives the sizes a Scan measured while it walked, so that a
// worktree need not be walked again to measure it.
type Sizes struct {
	mu      sync.Mutex
	entries map[string]*measured
	ended   bool
}

type measured struct {
	ready chan struct{}
	size  int64
	ok    bool
}

// NewSizes returns Sizes for one Scan.
func NewSizes() *Sizes {
	return &Sizes{entries: map[string]*measured{}}
}

// Wait returns the size in bytes of the regular files below the directory
// at path, symlinks left out, once the Scan measured it. It returns false if
// the Scan ended without measuring it, or ctx ended first.
func (s *Sizes) Wait(ctx context.Context, path string) (int64, bool) {
	m := s.entry(realPath(path))
	select {
	case <-m.ready:
		return m.size, m.ok
	case <-ctx.Done():
		return 0, false
	}
}

func (s *Sizes) entry(key string) *measured {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, ok := s.entries[key]
	if !ok {
		m = &measured{ready: make(chan struct{})}
		s.entries[key] = m
		if s.ended {
			close(m.ready)
		}
	}
	return m
}

func (s *Sizes) publish(dir string, size int64) {
	m := s.entry(realPath(dir))
	s.mu.Lock()
	defer s.mu.Unlock()
	if !m.ok && !s.ended {
		m.size, m.ok = size, true
		close(m.ready)
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
		if !m.ok {
			close(m.ready)
		}
	}
}
