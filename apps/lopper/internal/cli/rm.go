package cli

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"

	"github.com/spf13/cobra"

	"github.com/shinokamix/lopper/apps/lopper/internal/engine"
	"github.com/shinokamix/lopper/apps/lopper/internal/lopper"
)

func newRmCmd(eng *engine.Engine) *cobra.Command {
	var force, allSafe bool
	cmd := &cobra.Command{
		Use:   "rm path... | rm --all-safe [path...]",
		Short: "Remove worktrees, refusing ones that are not safe unless forced",
		Long: "rm removes the worktree at each given path whose folder still exists, if it " +
			"is safe to delete, checked just before removal; otherwise it is left alone and " +
			"why is printed. --force removes it anyway, whatever work it holds, even when " +
			"git lost track of it: moved by hand, orphaned or damaged. The branch is always " +
			"kept. A worktree whose folder is gone is not removed: only git's record of it " +
			"is left, and `git worktree prune` in its repository clears that.\n\n" +
			"--all-safe scans the given directories, or the ones lopper scans by default, " +
			"and removes every worktree under them that is safe and whose folder still " +
			"exists, each checked again just before removal. It never removes one that is " +
			"not safe, so it cannot be combined with --force.\n\n" +
			"It exits 2 when every worktree left was refused as not safe, and 1 on any " +
			"other failure.",
		Args: func(cmd *cobra.Command, args []string) error {
			if allSafe {
				return directories(cmd, args)
			}
			return cobra.MinimumNArgs(1)(cmd, args)
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			rm := remover{eng: eng, force: force}
			paths := args
			if allSafe {
				roots, err := physicalRoots(args)
				if err != nil {
					return err
				}
				if paths, err = safePaths(cmd, eng, roots); err != nil {
					return err
				}
			}
			failed, unsafe := 0, 0
			for _, a := range paths {
				err := rm.remove(cmd.Context(), a)
				if left, ok := errors.AsType[*engine.RecordLeftError](err); ok {
					fmt.Fprintf(cmd.OutOrStdout(), "removed %s; %v\n", a, left)
					continue
				}
				if err != nil {
					failed++
					if _, ok := errors.AsType[*engine.NotSafeError](err); ok {
						unsafe++
					}
					fmt.Fprintf(cmd.ErrOrStderr(), "%s: not removed: %v\n", a, err)
					continue
				}
				fmt.Fprintf(cmd.OutOrStdout(), "removed %s\n", a)
			}
			if failed > 0 {
				return &notRemovedError{failed: failed, unsafe: unsafe, total: len(paths), forceable: !allSafe}
			}
			return nil
		},
	}
	cmd.Flags().BoolVarP(&force, "force", "f", false, "remove worktrees that are not safe to delete too")
	cmd.Flags().BoolVar(&allSafe, "all-safe", false, "remove every safe worktree under the given directories, or the default ones")
	cmd.MarkFlagsMutuallyExclusive("force", "all-safe")
	return cmd
}

func safePaths(cmd *cobra.Command, eng *engine.Engine, roots []string) ([]string, error) {
	infos, err := statRoots(roots)
	if err != nil {
		return nil, err
	}
	opts := remembered(engine.Options{Roots: roots})
	found := map[lopper.ID]lopper.Worktree{}
	var paths []string
	for ev := range eng.Scan(cmd.Context(), opts) {
		switch ev := ev.(type) {
		case engine.WorktreeFound:
			found[ev.Worktree.ID] = ev.Worktree
		case engine.FactsUpdated:
			if wt := found[ev.ID]; ev.Final && ev.Verdict.Safe && wt.State != lopper.StateGone && under(infos, wt.Path) {
				paths = append(paths, wt.Path)
			}
		case engine.ScanDone:
			if ev.Err != nil {
				return nil, ev.Err
			}
		}
	}
	slices.Sort(paths)
	return paths, nil
}

type notRemovedError struct {
	failed, unsafe, total int
	forceable             bool
}

func (e *notRemovedError) Error() string {
	msg := fmt.Sprintf("%d of %d worktrees not removed", e.failed, e.total)
	if e.unsafe > 0 && e.forceable {
		msg += "; --force removes the ones not safe to delete"
	}
	return msg
}

type remover struct {
	eng   *engine.Engine
	force bool
}

var errGone = errors.New("no such directory; if a repository lists a worktree there, " +
	"`git worktree prune` in it removes the record")

func (r *remover) remove(ctx context.Context, path string) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	if _, err := os.Lstat(abs); errors.Is(err, fs.ErrNotExist) {
		return errGone
	}
	wt, err := r.eng.Find(ctx, []string{abs}, abs)
	if err != nil {
		return err
	}
	return r.eng.Remove(ctx, wt, r.force)
}

func statRoots(roots []string) ([]os.FileInfo, error) {
	infos := make([]os.FileInfo, 0, len(roots))
	for _, root := range roots {
		info, err := os.Stat(root)
		if err != nil {
			return nil, err
		}
		infos = append(infos, info)
	}
	return infos, nil
}
