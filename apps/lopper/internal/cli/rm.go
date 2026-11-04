package cli

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/shinokamix/lopper/apps/lopper/internal/engine"
)

func newRmCmd(eng *engine.Engine) *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "rm path...",
		Short: "Remove worktrees, refusing ones that are not safe unless forced",
		Long: "rm removes the worktree at each given path whose folder still exists, if it " +
			"is safe to delete, checked just before removal; otherwise it is left alone and " +
			"why is printed. --force removes it anyway, whatever work it holds, even when " +
			"git lost track of it: moved by hand, orphaned or damaged. The branch is always " +
			"kept. A worktree whose folder is gone is not removed: only git's record of it " +
			"is left, and `git worktree prune` in its repository clears that.",
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			rm := remover{eng: eng, force: force}
			failed, unsafe := 0, 0
			for _, a := range args {
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
				return &notRemovedError{failed: failed, unsafe: unsafe, total: len(args)}
			}
			return nil
		},
	}
	cmd.Flags().BoolVarP(&force, "force", "f", false, "remove worktrees that are not safe to delete too")
	return cmd
}

type notRemovedError struct {
	failed, unsafe, total int
}

func (e *notRemovedError) Error() string {
	msg := fmt.Sprintf("%d of %d worktrees not removed", e.failed, e.total)
	if e.unsafe > 0 {
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
