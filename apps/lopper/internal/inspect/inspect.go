// Package inspect collects Facts about a worktree. It never decides from
// them, which is verdict's job.
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
// Facts.Errors, so verdict refuses to call the worktree safe.
func (in Inspector) Quick(ctx context.Context, wt lopper.Worktree) lopper.Facts {
	var f lopper.Facts
	switch wt.State {
	case lopper.StateGone, lopper.StateOrphaned: // git cannot look inside
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
	// Without a base branch Merged stays nil, and verdict reports that.
	if base := wt.Repo.DefaultBranch; base != "" {
		if kind, err := in.mergeKind(ctx, wt.Path, base); err != nil {
			fail("compare with "+base, err)
		} else {
			f.Merged = &kind
		}
	}
	// git worktree remove checks status itself but not index flags, so read
	// them last, closest to removal.
	if n, err := in.uncheckedFiles(ctx, wt.Path); err != nil {
		fail("check index flags", err)
	} else {
		f.UncheckedFiles = &n
	}
	return f
}

// uncheckedFiles counts the files whose index flags hide edits from status.
// Assume-unchanged always hides them. Skip-worktree does when the path
// exists, since missing paths are normal in a sparse checkout. Without
// --sparse, git lists each file of a sparse index with its own flags.
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
		// The path is checked on disk below, so it must stay inside the worktree.
		for component := range strings.SplitSeq(path, string(filepath.Separator)) {
			if component == "" || component == "." || component == ".." {
				return 0, errors.New("invalid ls-files path")
			}
		}
		if tag >= 'a' && tag <= 'z' {
			unchecked[path] = true
		} else if tag == 'S' {
			_, err := os.Lstat(filepath.Join(dir, path))
			switch {
			case err == nil:
				unchecked[path] = true
			case errors.Is(err, fs.ErrNotExist), errors.Is(err, syscall.ENOTDIR):
				// A sparse checkout omits these files on purpose. A file in
				// place of their directory holds no copy of them.
			default:
				return 0, fmt.Errorf("check %q: %w", path, err)
			}
		}
	}
	return len(unchecked), nil
}

// Slow gathers the facts that need a walk of the directory. It walks in
// parallel, since a dependency tree can hold hundreds of thousands of files.
// The size leaves out unreadable or vanished entries below the root. An
// error at the root or a cancellation leaves the size unknown. A worktree
// git reports as gone takes no space.
// TODO: LastTouched from non-ignored files, busy processes.
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
	conf.ToSlash = false // paths are compared with wt.Path
	// Slow runs in one worker per worktree; keep each walk's own pool small.
	conf.NumWorkers = 4
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

// firstLine keeps error messages short, because git may add lines of hints.
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
