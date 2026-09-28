package cli

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/shinokamix/lopper/internal/config"
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
	roots []string // the default roots, loaded once, when a folder is gone
}

// remove removes the worktree at path, found as lopper scan would find it.
func (r *remover) remove(ctx context.Context, path string) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	var wt lopper.Worktree
	if _, statErr := os.Lstat(abs); errors.Is(statErr, fs.ErrNotExist) {
		wt, err = r.findGone(ctx, abs)
	} else {
		wt, err = r.eng.Find(ctx, []string{abs}, abs)
	}
	if err != nil {
		return err
	}
	return r.eng.Remove(ctx, wt, r.force)
}

// errGone is returned for a path whose folder is gone and that no
// repository found near it lists as a worktree.
var errGone = errors.New("no such directory, and no repository near it lists a worktree there; " +
	"if one does, `git worktree prune` in it removes the record")

// findGone finds the worktree at path, whose folder is gone: only its
// repository still knows it, and only a walk that meets the repository
// finds that. The default roots are walked first, as lopper scan does,
// then the folders above path, nearest first, since a repository usually
// sits near its worktrees. The climb ends at the first folder that holds a
// default root, such as the one holding the home directory, and never
// reaches the filesystem root: a repository further away is not near, and
// the walk would take long.
func (r *remover) findGone(ctx context.Context, path string) (lopper.Worktree, error) {
	if r.roots == nil {
		cfg, err := config.Default()
		if err != nil {
			return lopper.Worktree{}, err
		}
		r.roots = cfg.Roots
	}
	wt, err := r.eng.Find(ctx, r.roots, path)
	if !errors.Is(err, engine.ErrNotFound) {
		return wt, err
	}
	for dir := filepath.Dir(path); filepath.Dir(dir) != dir; dir = filepath.Dir(dir) {
		resolved, err := filepath.EvalSymlinks(dir) // as the roots are: /tmp is /private/tmp on macOS
		if err != nil {
			continue // gone too
		}
		if slices.ContainsFunc(r.roots, func(root string) bool { return within(resolved, root) }) {
			continue // walked already
		}
		wt, err := r.eng.Find(ctx, []string{dir}, path)
		if !errors.Is(err, engine.ErrNotFound) {
			return wt, err
		}
		if slices.ContainsFunc(r.roots, func(root string) bool { return within(root, resolved) }) {
			break
		}
	}
	return lopper.Worktree{}, errGone
}

// within reports whether path is dir or lies below it.
func within(path, dir string) bool {
	rel, err := filepath.Rel(dir, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
