package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/shinokamix/lopper/apps/lopper/internal/engine"
	"github.com/shinokamix/lopper/apps/lopper/internal/lopper"
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
			var r report
			for _, a := range args {
				err := rm.remove(cmd.Context(), a)
				if e, ok := errors.AsType[*engine.RecordLeftError](err); ok {
					r.left = append(r.left, recordLeft{path: a, repo: e.Repo})
					err = nil
				}
				if e, ok := errors.AsType[*engine.NotSafeError](err); ok {
					r.refused = append(r.refused, refusal{path: a, why: strings.Join(e.Verdict.Reasons(), ", "), unsafe: true})
					continue
				}
				if err != nil {
					r.refused = append(r.refused, refusal{path: a, why: err.Error(), gone: errors.Is(err, errGone)})
					continue
				}
				fmt.Fprintf(cmd.OutOrStdout(), "removed %s\n", a)
				r.removed = true
			}
			r.print(cmd.ErrOrStderr())
			if len(r.refused) > 0 {
				return fmt.Errorf("%d of %d worktrees not removed", len(r.refused), len(args))
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
}

// errGone is returned for a path whose folder is gone. Only git's record is
// left, in a repository the path does not lead to.
var errGone = errors.New("no such directory")

// remove removes the worktree at path, found as lopper scan would find it.
func (r *remover) remove(ctx context.Context, path string) error {
	wt, err := find(ctx, r.eng, path)
	if err != nil {
		return err
	}
	return r.eng.Remove(ctx, wt, r.force)
}

// find returns the worktree at path as lopper scan would find it.
func find(ctx context.Context, eng *engine.Engine, path string) (lopper.Worktree, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return lopper.Worktree{}, err
	}
	if _, err := os.Lstat(abs); errors.Is(err, fs.ErrNotExist) {
		return lopper.Worktree{}, errGone
	}
	return eng.Find(ctx, []string{abs}, abs)
}

// report is what rm prints after the removed worktrees, the way git lists
// what it refused and then hints at what to do.
type report struct {
	removed bool
	refused []refusal
	left    []recordLeft
}

type refusal struct {
	path, why string
	unsafe    bool // --force would remove it
	gone      bool // explain says how to clear what git still records
}

// recordLeft is a removed worktree whose record git kept.
type recordLeft struct{ path, repo string }

func (r report) print(w io.Writer) {
	var explain, unsafe bool
	if len(r.refused) > 0 {
		if r.removed {
			fmt.Fprintln(w)
		}
		fmt.Fprintln(w, "not removed:")
		tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		for _, f := range r.refused {
			fmt.Fprintf(tw, "  %s\t%s\n", f.path, f.why)
			explain = explain || f.unsafe || f.gone
			unsafe = unsafe || f.unsafe
		}
		_ = tw.Flush()
	}
	var hints []string
	if explain {
		hints = append(hints, "run `lopper explain -- <path>` to see what to do")
	}
	if unsafe {
		hints = append(hints, "run `lopper rm --force -- <path>...` to delete unsafe worktrees anyway")
	}
	for _, l := range r.left {
		hints = append(hints, "run `git -C <repository> worktree prune` to remove git's record of "+l.path+" in "+l.repo)
	}
	if len(hints) > 0 && (r.removed || len(r.refused) > 0) {
		fmt.Fprintln(w)
	}
	for _, h := range hints {
		fmt.Fprintln(w, "hint:", h)
	}
	if len(hints) > 0 {
		fmt.Fprintln(w, "hint:", commandTemplates)
	}
}
