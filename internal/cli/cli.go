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

	"github.com/shinokamix/lopper/internal/catalog"
	"github.com/shinokamix/lopper/internal/engine"
	"github.com/shinokamix/lopper/internal/tui"
	"github.com/shinokamix/lopper/internal/update"
)

// NewRoot returns the root command. releaseVersion is the installed
// release version, or "dev" to disable updates for a local build.
func NewRoot(releaseVersion string) *cobra.Command {
	eng := engine.New()
	root := &cobra.Command{
		Use:   "lopper [path...]",
		Short: "Clean up the Git worktrees you and your agents left behind",
		Args:  directories,
		Long:  "lopper scans your disk for git worktrees and tells you which ones are safe to delete — and why.",
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			return eng.Check(cmd.Context())
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			opts, err := scanOptions(args)
			if err != nil {
				return err
			}
			updates, stop := offerUpdates(cmd.Context(), releaseVersion)
			defer stop()
			return tui.Run(cmd.Context(), eng, opts, updates)
		},
	}
	root.AddCommand(newScanCmd(eng), newRmCmd(eng), newUpdateCmd(releaseVersion))
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
	if len(args) == 0 {
		roots, err := defaultRoots()
		return remembered(engine.Options{Roots: roots}), err
	}
	roots := make([]string, 0, len(args))
	for _, a := range args {
		abs, err := filepath.Abs(a)
		if err != nil {
			return engine.Options{}, err
		}
		roots = append(roots, abs)
	}
	return remembered(engine.Options{Roots: roots}), nil
}

// remembered makes a scan start with the repositories earlier scans met and
// remember the ones it meets, unless LOPPER_NO_CACHE is set. An unreadable or
// unwritable cache only makes the scan as slow as without one.
func remembered(opts engine.Options) engine.Options {
	if os.Getenv("LOPPER_NO_CACHE") != "" {
		return opts
	}
	c := catalog.Open()
	opts.Known = c.Known()
	opts.Remember = func(places []string) { _ = c.Remember(opts.Roots, places) }
	return opts
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
		Current:  "v" + strings.TrimPrefix(version, "v"),
		Latest:   latest,
		Repo:     update.Repo,
		Install:  u.Install,
		Postpone: u.Postpone,
		Skip:     u.Skip,
		Restart:  func() error { stop(); return u.Restart() },
	}, stop
}
