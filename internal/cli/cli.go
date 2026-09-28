// Package cli defines the command tree. Running `lopper` without a
// subcommand opens the TUI; subcommands are for scripts and CI.
package cli

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/shinokamix/lopper/internal/config"
	"github.com/shinokamix/lopper/internal/engine"
	"github.com/shinokamix/lopper/internal/tui"
)

// NewRoot returns the root command of lopper at version, the release it
// was built from.
func NewRoot(version string) *cobra.Command {
	root := &cobra.Command{
		Use:   "lopper [path...]",
		Short: "Find and safely remove stale git worktrees",
		Args:  directories,
		Long:  "lopper scans your disk for git worktrees and tells you which ones are safe to delete — and why.",
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			return engine.New().Check(cmd.Context())
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			opts, err := scanOptions(args)
			if err != nil {
				return err
			}
			return tui.Run(cmd.Context(), engine.New(), opts)
		},
	}
	root.AddCommand(newScanCmd(), newRmCmd(), newUpdateCmd(version))
	return root
}

// directories rejects path arguments that are not existing directories,
// before any scanning starts.
func directories(_ *cobra.Command, args []string) error {
	for _, a := range args {
		info, err := os.Stat(a)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			return fmt.Errorf("%s: no such directory", a)
		case err != nil:
			return err
		case !info.IsDir():
			return fmt.Errorf("%s: not a directory", a)
		}
	}
	return nil
}

func scanOptions(args []string) (engine.Options, error) {
	cfg, err := config.Default()
	if err != nil {
		return engine.Options{}, err
	}
	roots := cfg.Roots
	if len(args) > 0 {
		roots = make([]string, 0, len(args))
		for _, a := range args {
			abs, err := filepath.Abs(a)
			if err != nil {
				return engine.Options{}, err
			}
			roots = append(roots, abs)
		}
	}
	return engine.Options{Roots: roots}, nil
}
