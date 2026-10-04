// Package inspect collects Facts about a worktree. It observes and
// never decides: turning facts into a recommendation is verdict's job.
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

	"github.com/shinokamix/lopper/internal/gitx"
	"github.com/shinokamix/lopper/internal/lopper"
)

// Inspector gathers facts about worktrees by running Git.
type Inspector struct {
	Git gitx.Runner
}

// Quick gathers git facts: working tree state, index flags, and merge status.
// A fact that git cannot provide stays nil and the git error is recorded
// in Facts.Errors, so verdict can refuse to call the worktree safe.
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
	// Commits reachable from HEAD but neither on a remote nor in the base branch.
	args := []string{"rev-list", "--count", "HEAD", "--not", "--remotes"}
	if base := wt.Repo.DefaultBranch; base != "" {
		args = append(args, base)
	}
	if n, err := in.count(ctx, wt.Path, args...); err != nil {
		fail("count unpushed commits", err)
	} else {
		f.Unpushed = &n
	}
	// Without a base branch Merged stays nil; verdict explains why.
	if base := wt.Repo.DefaultBranch; base != "" {
		if kind, err := in.mergeKind(ctx, wt.Path, base); err != nil {
			fail("compare with "+base, err)
		} else {
			f.Merged = &kind
		}
	}
	// git worktree remove checks status itself, but not index flags: read
	// them last, the closest to removal.
	if n, err := in.uncheckedFiles(ctx, wt.Path); err != nil {
		fail("check index flags", err)
	} else {
		f.UncheckedFiles = &n
	}
	return f
}

// Index flags can hide edits from status. Assume-unchanged always leaves
// a file unchecked. Skip-worktree does too when a path exists, but missing
// paths are normal in a sparse checkout. Without --sparse, git lists each
// file of a sparse index on its own, with its own flags.
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
		// The path is looked up below, so it must stay inside the worktree.
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
				// Git deliberately omits these files in sparse checkouts. A
				// file in place of their directory holds no copy of them.
			default:
				return 0, fmt.Errorf("check %q: %w", path, err)
			}
		}
	}
	return len(unchecked), nil
}

// Slow gathers expensive facts that require walking the directory, which
// it reads in parallel: a dependency tree holds hundreds of thousands of
// files. Size excludes unreadable or vanished entries below the root. A root
// error or cancellation leaves the size unknown; a worktree git reports
// as gone takes no space.
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

// firstLine keeps error messages short: git may print several lines of hints.
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
