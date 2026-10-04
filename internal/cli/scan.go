package cli

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/shinokamix/lopper/internal/engine"
	"github.com/shinokamix/lopper/internal/lopper"
)

type scanRecord struct {
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

// scanFacts is how `scan --json` prints [lopper.Facts]. Scripts rely on this
// format, so it lives here, where renaming a core field cannot change it.
type scanFacts struct {
	Dirty          *int              `json:"dirty,omitempty"`
	UncheckedFiles *int              `json:"unchecked_files,omitempty"`
	Unpushed       *int              `json:"unpushed,omitempty"`
	Merged         *lopper.MergeKind `json:"merged,omitempty"`
	SizeBytes      *int64            `json:"size_bytes,omitempty"`
	Errors         []string          `json:"errors,omitempty"`
}

func newScanFacts(f lopper.Facts) scanFacts {
	return scanFacts{
		Dirty:          f.Dirty,
		UncheckedFiles: f.UncheckedFiles,
		Unpushed:       f.Unpushed,
		Merged:         f.Merged,
		SizeBytes:      f.SizeBytes,
		Errors:         f.Errors,
	}
}

func newScanCmd(eng *engine.Engine) *cobra.Command {
	var asJSON bool
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
					}
					order = append(order, wt.ID)
				case engine.FactsUpdated:
					r := records[ev.ID]
					if r == nil {
						continue // not announced by WorktreeFound; nothing to attach to
					}
					r.Facts, r.Safe = newScanFacts(ev.Facts), ev.Safe
				case engine.ScanDone:
					if ev.Err != nil {
						return ev.Err
					}
				}
			}

			out := make([]*scanRecord, 0, len(order))
			for _, id := range order {
				out = append(out, records[id])
			}
			w := cmd.OutOrStdout()
			if asJSON {
				enc := json.NewEncoder(w)
				enc.SetIndent("", "  ")
				return enc.Encode(out)
			}
			// TODO: pretty table output via lipgloss/table.
			for _, r := range out {
				safe := "unsafe"
				if r.Safe {
					safe = "safe"
				}
				fmt.Fprintf(w, "%-7s %-40s %s\n", safe, r.Branch, r.Path)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print results as JSON")
	return cmd
}
