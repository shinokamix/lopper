// Command lopper finds and safely removes stale git worktrees.
package main

import (
	"context"
	"os"
	"os/signal"
	"runtime/debug"
	"syscall"

	"github.com/charmbracelet/fang"

	"github.com/shinokamix/lopper/internal/cli"
)

// Set by goreleaser via -ldflags.
var (
	version = "dev"
	commit  = ""
)

func main() {
	os.Exit(run())
}

func run() int {
	// SIGTERM exists on every OS in package syscall; on Windows it is
	// simply never delivered.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	v, release := buildVersions()
	if err := fang.Execute(ctx, cli.NewRoot(release), fang.WithVersion(v), fang.WithCommit(commit)); err != nil {
		return 1
	}
	return 0
}

// buildVersions returns the displayed version and the version eligible
// for updates. GoReleaser stamps version; go install records the module
// version. Local builds record VCS settings, even on a clean release tag.
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
