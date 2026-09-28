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
	v := buildVersion()
	if err := fang.Execute(ctx, cli.NewRoot(v), fang.WithVersion(v), fang.WithCommit(commit)); err != nil {
		return 1
	}
	return 0
}

// buildVersion falls back to the module version recorded by
// `go install …@version` when goreleaser did not set one.
func buildVersion() string {
	if version != "dev" {
		return version
	}
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return version
}
