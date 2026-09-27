package discovery

import (
	"testing"

	"github.com/shinokamix/lopper/internal/lopper"
)

func TestClassifyOrigin(t *testing.T) {
	cases := map[string]lopper.Origin{
		"/home/u/proj/.claude/worktrees/fix-bug": lopper.OriginClaudeCode,
		"/home/u/.codex/worktrees/abc":           lopper.OriginCodex,
		"/home/u/proj-feature":                   lopper.OriginManual,
		"/home/u/conductor/workspaces/proj/x":    lopper.OriginConductor,
		`C:\Users\u\proj\.claude\worktrees\x`:    lopper.OriginClaudeCode,
		`C:\Users\u\.codex\worktrees\abc`:        lopper.OriginCodex,
		`C:\Users\u\conductor\workspaces\proj\x`: lopper.OriginConductor,
		`C:\Users\u\proj-feature`:                lopper.OriginManual,
	}
	for path, want := range cases {
		if got := classifyOrigin(path); got != want {
			t.Errorf("classifyOrigin(%q) = %q, want %q", path, got, want)
		}
	}
}
