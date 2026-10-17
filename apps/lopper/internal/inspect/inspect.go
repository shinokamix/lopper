// Package inspect collects Facts about a worktree.
package inspect

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"

	"github.com/charlievieth/fastwalk"

	"github.com/shinokamix/lopper/apps/lopper/internal/gitx"
	"github.com/shinokamix/lopper/apps/lopper/internal/lopper"
)

// Inspector gathers facts about worktrees by running git.
type Inspector struct {
	Git gitx.Runner
}

// Quick gathers the facts git reports: working tree state, index flags and
// merge status. A fact git cannot provide stays nil and its error goes to
// Facts.Errors.
func (in Inspector) Quick(ctx context.Context, wt lopper.Worktree) lopper.Facts {
	var f lopper.Facts
	switch wt.State {
	case lopper.StateGone, lopper.StateOrphaned:
		return f
	case lopper.StateTracked, lopper.StateMoved, lopper.StateUnconfirmed:
	}
	fail := func(what string, err error) {
		f.Errors = append(f.Errors, "could not "+what+": "+firstLine(err.Error()))
	}

	if out, err := in.Git.Run(ctx, wt.Path, "status", "--porcelain", "--untracked-files=normal"); err != nil {
		fail("read status", err)
	} else {
		f.Dirty = new(countLines(out))
	}
	args := []string{"rev-list", "--count", "HEAD", "--not", "--remotes"}
	if base := wt.Repo.DefaultBranch; base != "" {
		args = append(args, base)
	}
	if n, err := in.count(ctx, wt.Path, args...); err != nil {
		fail("count unpushed commits", err)
	} else {
		f.Unpushed = &n
	}
	if base := wt.Repo.DefaultBranch; base != "" {
		if kind, err := in.mergeKind(ctx, wt.Path, base); err != nil {
			fail("compare with "+base, err)
		} else {
			f.Merged = &kind
		}
	}
	// git worktree remove checks status but not index flags, so they are
	// read last, closest to removal.
	if n, err := in.uncheckedFiles(ctx, wt.Path); err != nil {
		fail("check index flags", err)
	} else {
		f.UncheckedFiles = &n
	}
	return f
}

// uncheckedFiles counts files whose index flags hide edits. Assume-unchanged
// always hides them. Skip-worktree hides them when the path exists, since
// missing paths are normal in a sparse checkout. Without --sparse, git lists
// each file of a sparse index with its own flags.
func (in Inspector) uncheckedFiles(ctx context.Context, dir string) (int, error) {
	out, err := in.Git.Run(ctx, dir, "ls-files", "--cached", "-v", "-z")
	if err != nil {
		return 0, err
	}
	unchecked := make(map[string]bool)
	for out != "" {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		var record string
		var terminated bool
		record, out, terminated = strings.Cut(out, "\x00")
		if !terminated || len(record) < 3 || record[1] != ' ' || !strings.ContainsRune("HSMRCK?hsmrck", rune(record[0])) {
			return 0, errors.New("invalid ls-files record")
		}
		tag, path := record[0], filepath.FromSlash(record[2:])
		for component := range strings.SplitSeq(path, string(filepath.Separator)) {
			if component == "" || component == "." || component == ".." {
				return 0, errors.New("invalid ls-files path")
			}
		}
		if tag >= 'a' && tag <= 'z' {
			unchecked[path] = true
		} else if tag == 'S' {
			ok, err := exists(filepath.Join(dir, path))
			if err != nil {
				return 0, fmt.Errorf("check %q: %w", path, err)
			}
			if ok {
				unchecked[path] = true
			}
		}
	}
	return len(unchecked), nil
}

func exists(path string) (bool, error) {
	_, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENOTDIR) {
		return false, nil
	}
	return err == nil, err
}

const walkWorkersPerWorktree = 4

// Slow measures the size of the worktree's directory. It leaves out
// unreadable or vanished entries below the root. An error at the root or a
// cancellation leaves the size unknown. A gone worktree takes no space.
func (in Inspector) Slow(ctx context.Context, wt lopper.Worktree, f lopper.Facts) lopper.Facts {
	switch wt.State {
	case lopper.StateGone:
		f.SizeBytes = new(int64(0))
		return f
	case lopper.StateTracked, lopper.StateOrphaned, lopper.StateMoved, lopper.StateUnconfirmed:
	}
	var size atomic.Int64
	f.SizeBytes = nil
	conf := fastwalk.DefaultConfig
	conf.ToSlash = false
	conf.NumWorkers = walkWorkersPerWorktree
	err := fastwalk.Walk(&conf, wt.Path, func(path string, d fs.DirEntry, err error) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			if path == wt.Path {
				return err
			}
			return nil
		}
		if d.Type().IsRegular() {
			if info, err := d.Info(); err == nil {
				size.Add(info.Size())
			}
		}
		return nil
	})
	if err != nil {
		f.Errors = append(f.Errors, "could not measure size: "+firstLine(err.Error()))
		return f
	}
	f.SizeBytes = new(size.Load())
	return f
}

func (in Inspector) count(ctx context.Context, dir string, args ...string) (int, error) {
	out, err := in.Git.Run(ctx, dir, args...)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(out))
}

// firstLine drops the hint lines git adds to errors.
func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return line
}

func countLines(s string) int {
	if s == "" {
		return 0
	}
	return strings.Count(s, "\n") + 1
}
