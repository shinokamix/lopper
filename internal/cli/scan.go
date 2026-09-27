package cli

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/shinokamix/lopper/internal/engine"
	"github.com/shinokamix/lopper/internal/lopper"
)

type scanRecord struct {
	Path    string          `json:"path"`
	Repo    string          `json:"repo"`
	Branch  string          `json:"branch,omitempty"`
	Origin  lopper.Origin   `json:"origin"`
	Verdict lopper.Level    `json:"verdict"`
	Reasons []lopper.Reason `json:"reasons"`
	Facts   lopper.Facts    `json:"facts"`
}

func newScanCmd() *cobra.Command {
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

			for ev := range engine.New().Scan(cmd.Context(), opts) {
				switch ev := ev.(type) {
				case engine.WorktreeFound:
					wt := ev.Worktree
					records[wt.ID] = &scanRecord{Path: wt.Path, Repo: wt.Repo.Path, Branch: wt.Branch, Origin: wt.Origin}
					order = append(order, wt.ID)
				case engine.FactsUpdated:
					r := records[ev.ID]
					if r == nil {
						continue // not announced by WorktreeFound; nothing to attach to
					}
					r.Facts, r.Verdict, r.Reasons = ev.Facts, ev.Verdict.Level, ev.Verdict.Reasons
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
				fmt.Fprintf(w, "%-7s %-40s %s\n", r.Verdict, r.Branch, r.Path)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print results as JSON")
	return cmd
}
