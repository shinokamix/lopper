package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/shinokamix/lopper/apps/lopper/internal/engine"
	"github.com/shinokamix/lopper/apps/lopper/internal/lopper"
)

type scanRecord struct {
	branchLabel string
	Path        string    `json:"path"`
	Repo        string    `json:"repo"`
	Branch      string    `json:"branch,omitempty"`
	Safe        bool      `json:"safe"`
	Locked      bool      `json:"locked,omitempty"`
	Prunable    bool      `json:"prunable,omitempty"`
	Orphaned    bool      `json:"orphaned,omitempty"`
	MovedFrom   string    `json:"moved_from,omitempty"`
	Unconfirmed string    `json:"unconfirmed,omitempty"`
	Facts       scanFacts `json:"facts"`
}

type scanFacts struct {
	Dirty          *int              `json:"dirty,omitempty"`
	UncheckedFiles *int              `json:"unchecked_files,omitempty"`
	Unpushed       *int              `json:"unpushed,omitempty"`
	Merged         *lopper.MergeKind `json:"merged,omitempty"`
	LastCommit     *time.Time        `json:"last_commit,omitempty"`
	UpstreamGone   *bool             `json:"upstream_gone,omitempty"`
	SizeBytes      *int64            `json:"size_bytes,omitempty"`
	Errors         []string          `json:"errors,omitempty"`
}

func newScanFacts(f lopper.Facts) scanFacts {
	return scanFacts{
		Dirty:          f.Dirty,
		UncheckedFiles: f.UncheckedFiles,
		Unpushed:       f.Unpushed,
		Merged:         f.Merged,
		LastCommit:     f.LastCommit,
		UpstreamGone:   f.UpstreamGone,
		SizeBytes:      f.SizeBytes,
		Errors:         f.Errors,
	}
}

func newScanCmd(eng *engine.Engine) *cobra.Command {
	var asJSON, safeOnly, pathsOnly bool
	var olderThan age
	cmd := &cobra.Command{
		Use:   "scan [path...]",
		Short: "Scan for worktrees without the interactive UI",
		Args:  directories,
		RunE: func(cmd *cobra.Command, args []string) error {
			opts, err := scanOptions(args)
			if err != nil {
				return err
			}
			records := map[lopper.ID]*scanRecord{}
			var order []lopper.ID

			for ev := range eng.Scan(cmd.Context(), opts) {
				switch ev := ev.(type) {
				case engine.WorktreeFound:
					wt := ev.Worktree
					records[wt.ID] = &scanRecord{
						Path: wt.Path, Repo: wt.Repo.Path, Branch: wt.Branch,
						Locked: wt.Locked, Prunable: wt.State == lopper.StateGone, Orphaned: wt.State == lopper.StateOrphaned,
						MovedFrom: wt.MovedFrom, Unconfirmed: wt.Reason,
						branchLabel: wt.BranchName(),
					}
					order = append(order, wt.ID)
				case engine.FactsUpdated:
					r := records[ev.ID]
					if r == nil {
						continue
					}
					r.Facts, r.Safe = newScanFacts(ev.Facts), ev.Verdict.Safe
				case engine.ScanDone:
					if ev.Err != nil {
						return ev.Err
					}
				}
			}

			now := time.Now()
			out := make([]*scanRecord, 0, len(order))
			for _, id := range order {
				r := records[id]
				if safeOnly && !r.Safe {
					continue
				}
				if !cmd.Flags().Changed("older-than") || olderThan.before(r.Facts.LastCommit, now) {
					out = append(out, r)
				}
			}
			w := cmd.OutOrStdout()
			if asJSON {
				enc := json.NewEncoder(w)
				enc.SetIndent("", "  ")
				return enc.Encode(out)
			}
			if pathsOnly {
				for _, r := range out {
					if _, err := fmt.Fprintln(w, r.Path); err != nil {
						return err
					}
				}
				return nil
			}
			for _, r := range out {
				safe := "unsafe"
				if r.Safe {
					safe = "safe"
				}
				fmt.Fprintf(w, "%-7s %-40s %s\n", safe, r.branchLabel, r.Path)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print results as JSON")
	cmd.Flags().BoolVar(&safeOnly, "safe", false, "list only worktrees safe to delete")
	cmd.Flags().BoolVar(&pathsOnly, "paths", false, "print only paths, one per line")
	cmd.MarkFlagsMutuallyExclusive("json", "paths")
	cmd.Flags().Var(&olderThan, "older-than", "list only worktrees whose last commit is older than this many hours, days or weeks, such as 12h, 30d or 8w")
	return cmd
}

type age struct {
	text string
	d    time.Duration
}

var ageUnits = map[string]time.Duration{"h": time.Hour, "d": 24 * time.Hour, "w": 7 * 24 * time.Hour}

func (a *age) Set(s string) error {
	digits := strings.TrimRight(s, "hdw")
	unit, ok := ageUnits[s[len(digits):]]
	n, err := strconv.ParseInt(digits, 10, 64)
	if !ok || err != nil || n < 0 || n > math.MaxInt64/int64(unit) {
		return errors.New("want a whole number of hours, days or weeks, such as 12h, 30d or 8w")
	}
	a.text, a.d = s, time.Duration(n)*unit
	return nil
}

func (a *age) String() string { return a.text }

func (a *age) Type() string { return "age" }

func (a *age) before(commit *time.Time, now time.Time) bool {
	return commit != nil && now.Sub(*commit) > a.d
}
