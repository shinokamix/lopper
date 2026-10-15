package cli

import (
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/shinokamix/lopper/apps/lopper/internal/engine"
	"github.com/shinokamix/lopper/apps/lopper/internal/lopper"
)

// shownIgnored is how many ignored paths explain names before it counts the
// rest.
const shownIgnored = 10

const commandTemplates = "Commands are templates. Replace placeholders and quote values for your shell."

func newExplainCmd(eng *engine.Engine) *cobra.Command {
	return &cobra.Command{
		Use:   "explain path",
		Short: "Show what lopper checked in a worktree and why it is safe to delete or not",
		Long: "explain inspects the worktree at path as rm would and prints what it found: " +
			"its state, the base branch it is compared with, each check and its result, " +
			"what each reason not to delete it takes to clear, and what lopper does not check.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			wt, err := find(cmd.Context(), eng, args[0])
			if err != nil {
				if errors.Is(err, errGone) {
					return fmt.Errorf("%s: %w; if a repository still lists a worktree there, "+
						"`git worktree prune` in it removes its record", args[0], err)
				}
				return fmt.Errorf("%s: %w", args[0], err)
			}
			f, v := eng.Inspect(cmd.Context(), wt)
			explain(cmd.OutOrStdout(), wt, f, v)
			return nil
		},
	}
}

func explain(w io.Writer, wt lopper.Worktree, f lopper.Facts, v lopper.Verdict) {
	base := wt.Repo.DefaultBranch
	if base == "" {
		base = "none found"
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintf(tw, "worktree\t%s\n", wt.Path)
	fmt.Fprintf(tw, "repository\t%s\n", wt.Repo.Path)
	fmt.Fprintf(tw, "branch\t%s\n", wt.Ref())
	fmt.Fprintf(tw, "state\t%s\n", state(wt))
	fmt.Fprintf(tw, "base\t%s\n", base)
	_ = tw.Flush()
	fmt.Fprintln(w)
	merged := "?"
	if f.Merged != nil {
		merged = map[lopper.MergeKind]string{
			lopper.NotMerged:    "no",
			lopper.MergedFF:     "yes, its commits are in " + wt.Repo.DefaultBranch,
			lopper.MergedSquash: "yes, " + wt.Repo.DefaultBranch + " has its changes after a squash, rebase or cherry-pick",
		}[*f.Merged]
	}
	size := "?"
	if f.SizeBytes != nil {
		size = lopper.FormatSize(*f.SizeBytes)
	}
	tw = tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintf(tw, "uncommitted\t%s\tentries git status lists\n", count(f.Dirty))
	fmt.Fprintf(tw, "unchecked\t%s\tfiles whose index flags hide edits from git status\n", count(f.UncheckedFiles))
	fmt.Fprintf(tw, "unpushed\t%s\tcommits on no remote branch and not in the base branch\n", count(f.Unpushed))
	fmt.Fprintf(tw, "merged\t%s\n", merged)
	fmt.Fprintf(tw, "size\t%s\n", size)
	fmt.Fprintf(tw, "ignored\t%s\n", ignored(f.Ignored))
	_ = tw.Flush()
	for _, e := range f.Errors {
		fmt.Fprintln(w, e)
	}
	fmt.Fprintln(w)
	if v.Safe {
		fmt.Fprintln(w, "safe to delete")
	} else {
		fmt.Fprintln(w, "not safe to delete")
		fmt.Fprintln(w, commandTemplates)
		for _, n := range v.Notes {
			if n.Blocks {
				fmt.Fprintf(w, "  %s: %s\n", n.Text, n.Hint)
			}
		}
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, "lopper does not check:")
	fmt.Fprintln(w, "  ignored files, which go with the worktree")
	fmt.Fprintln(w, "  the remote itself: it compares with the refs the last `git fetch` left")
	if wt.Branch != "" {
		fmt.Fprintf(w, "removing the worktree keeps the branch %s\n", wt.Branch)
	}
}

func state(wt lopper.Worktree) string {
	s := map[lopper.State]string{
		lopper.StateTracked:     "tracked by git",
		lopper.StateGone:        "folder gone, only git's record is left",
		lopper.StateOrphaned:    "not tracked by git: its repository no longer lists it",
		lopper.StateMoved:       "moved by hand from " + wt.MovedFrom,
		lopper.StateUnconfirmed: "not confirmed by git: " + wt.Reason,
	}[wt.State]
	if wt.Locked {
		s += ", locked"
	}
	return s
}

func count(n *int) string {
	if n == nil {
		return "?"
	}
	return strconv.Itoa(*n)
}

func ignored(paths []string) string {
	switch {
	case paths == nil:
		return "?"
	case len(paths) == 0:
		return "none"
	case len(paths) > shownIgnored:
		return fmt.Sprintf("%s and %d more", strings.Join(paths[:shownIgnored], ", "), len(paths)-shownIgnored)
	}
	return strings.Join(paths, ", ")
}
