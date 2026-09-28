package cli

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/shinokamix/lopper/internal/config"
	"github.com/shinokamix/lopper/internal/engine"
)

func newRmCmd() *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "rm path...",
		Short: "Remove worktrees, refusing ones that are not safe unless forced",
		Long: "rm removes each worktree at the given paths if it is safe to delete, checked " +
			"just before removal; otherwise it is left alone and why is printed. --force " +
			"removes it anyway, whatever it holds or however git lost track of it: any " +
			"worktree lopper scan shows. The branch is always kept.",
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			rm := remover{eng: engine.New(), force: force}
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
			switch {
			case unsafe > 0:
				return fmt.Errorf("%d of %d worktrees not removed; --force removes the ones not safe to delete", failed, len(args))
			case failed > 0:
				return fmt.Errorf("%d of %d worktrees not removed", failed, len(args))
			}
			return nil
		},
	}
	cmd.Flags().BoolVarP(&force, "force", "f", false, "remove worktrees that are not safe to delete too")
	return cmd
}

type remover struct {
	eng   *engine.Engine
	force bool
	roots []string // where to look for worktrees whose directory is gone; loaded once
}

// remove removes the worktree at path, found as lopper scan would find it.
func (r *remover) remove(ctx context.Context, path string) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	search := []string{abs}
	if _, err := os.Lstat(abs); errors.Is(err, fs.ErrNotExist) {
		// Only its repository still knows it, and only a scan finds that.
		if r.roots == nil {
			cfg, err := config.Default()
			if err != nil {
				return err
			}
			r.roots = cfg.Roots
		}
		search = r.roots
	}
	wt, err := r.eng.Find(ctx, search, abs)
	if err != nil {
		return err
	}
	return r.eng.Remove(ctx, wt, r.force)
}
