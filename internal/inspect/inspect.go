// Package inspect collects Facts about a worktree. It observes and
// never decides: turning facts into a recommendation is verdict's job.
package inspect

import (
	"context"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/shinokamix/lopper/internal/gitx"
	"github.com/shinokamix/lopper/internal/lopper"
)

type Inspector struct {
	Git gitx.Runner
}

// Quick gathers cheap facts: working tree state and merge status.
// A fact that git cannot provide stays nil and the git error is recorded
// in Facts.Errors, so verdict can refuse to call the worktree safe.
func (in Inspector) Quick(ctx context.Context, wt lopper.Worktree) lopper.Facts {
	var f lopper.Facts
	if wt.Prunable {
		return f
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
		if n, err := in.count(ctx, wt.Path, "rev-list", "--count", base+"..HEAD"); err != nil {
			fail("compare with "+base, err)
		} else {
			kind := lopper.NotMerged
			if n == 0 {
				kind = lopper.MergedFF
			}
			// TODO: detect squash/rebase merges via `git patch-id`.
			f.Merged = &kind
		}
	}
	return f
}

// Slow gathers expensive facts that require walking the directory.
// Size excludes unreadable or vanished entries below the root. A root
// error or cancellation leaves the size unknown.
// TODO: LastTouched from non-ignored files, busy processes.
func (in Inspector) Slow(ctx context.Context, wt lopper.Worktree, f lopper.Facts) lopper.Facts {
	if wt.Prunable {
		return f
	}
	var size int64
	f.SizeBytes = nil
	err := filepath.WalkDir(wt.Path, func(path string, d fs.DirEntry, err error) error {
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
				size += info.Size()
			}
		}
		return nil
	})
	if err != nil {
		f.Errors = append(f.Errors, "could not measure size: "+firstLine(err.Error()))
		return f
	}
	f.SizeBytes = &size
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
