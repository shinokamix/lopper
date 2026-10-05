// Command lopper finds and safely removes stale git worktrees.
package main

import (
	"context"
	"os"
	"os/signal"
	"runtime/debug"
	"syscall"

	"github.com/charmbracelet/fang"

	"github.com/shinokamix/lopper/apps/lopper/internal/cli"
)

// goreleaser sets these with -ldflags.
var (
	version = "dev"
	commit  = ""
)

func main() {
	os.Exit(run())
}

func run() int {
	// syscall defines SIGTERM on every OS. Windows never delivers it.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	v, release := buildVersions()
	if err := fang.Execute(ctx, cli.NewRoot(release), fang.WithVersion(v), fang.WithCommit(commit)); err != nil {
		return 1
	}
	return 0
}

// buildVersions returns the version to display and the version to update
// from. GoReleaser stamps version, and go install records the module
// version. A local build records VCS settings even on a clean release tag,
// so it gets no updates.
func buildVersions() (display, release string) {
	if version != "dev" {
		return version, version
	}
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		for _, setting := range info.Settings {
			if setting.Key == "vcs" {
				return info.Main.Version, "dev"
			}
		}
		return info.Main.Version, info.Main.Version
	}
	return version, version
}
