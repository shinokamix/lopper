// Package cli defines the command tree. Running `lopper` without a
// subcommand opens the TUI; subcommands are for scripts and CI.
package cli

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/shinokamix/lopper/internal/config"
	"github.com/shinokamix/lopper/internal/engine"
	"github.com/shinokamix/lopper/internal/tui"
	"github.com/shinokamix/lopper/internal/update"
)

// NewRoot returns the root command. releaseVersion is the installed
// release version, or "dev" to disable updates for a local build.
func NewRoot(releaseVersion string) *cobra.Command {
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
			updates, stop := offerUpdates(cmd.Context(), releaseVersion)
			defer stop()
			return tui.Run(cmd.Context(), engine.New(), opts, updates)
		},
	}
	root.AddCommand(newScanCmd(), newRmCmd(), newUpdateCmd(releaseVersion))
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

// offerUpdates lets the TUI offer a newer release that an earlier start
// found, and looks for one in the background until stop, for the next
// start to offer. It does neither if lopper was built from source or
// LOPPER_NO_UPDATE_CHECK is set.
func offerUpdates(ctx context.Context, version string) (updates tui.Updates, stop func()) {
	if !update.Released(version) || os.Getenv("LOPPER_NO_UPDATE_CHECK") != "" {
		return tui.Updates{}, func() {}
	}
	u, err := update.New()
	if err != nil {
		return tui.Updates{}, func() {}
	}
	latest := u.Available(version) // before Refresh changes what it reads
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		u.Refresh(ctx)
	}()
	stop = func() { cancel(); <-done }
	return tui.Updates{
		Current: "v" + strings.TrimPrefix(version, "v"),
		Latest:  latest,
		Repo:    update.Repo,
		Install: u.Install,
		Skip:    u.Skip,
		Restart: func() error { stop(); return u.Restart() },
	}, stop
}
