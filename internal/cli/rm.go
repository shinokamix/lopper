package cli

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/shinokamix/lopper/internal/engine"
	"github.com/shinokamix/lopper/internal/lopper"
)

func newRmCmd() *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "rm path...",
		Short: "Remove worktrees, refusing ones that are not safe unless forced",
		Long: "rm removes each worktree at the given paths if it is safe to delete, checked " +
			"just before removal; otherwise it is left alone and why is printed. --force " +
			"removes it anyway, with whatever work it holds. The branch is always kept.",
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			eng := engine.New()
			failed, unsafe := 0, 0
			for _, a := range args {
				if err := removeOne(cmd, eng, a, force); err != nil {
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

func removeOne(cmd *cobra.Command, eng *engine.Engine, path string, force bool) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	if _, err := os.Stat(abs); errors.Is(err, fs.ErrNotExist) {
		return errors.New("no such directory")
	}
	return eng.Remove(cmd.Context(), lopper.Worktree{Path: abs}, force)
}
