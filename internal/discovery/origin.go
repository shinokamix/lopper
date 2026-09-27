package discovery

import (
	"strings"

	"github.com/shinokamix/lopper/internal/lopper"
)

// originMarkers map a path fragment to the tool that creates worktrees there.
// TODO: Gemini CLI, Qwen Code, Cursor, Superset, intent, bb.
var originMarkers = []struct {
	fragment string
	origin   lopper.Origin
}{
	{"/.claude/worktrees/", lopper.OriginClaudeCode},
	{"/.codex/worktrees/", lopper.OriginCodex},
	{"/conductor/", lopper.OriginConductor},
}

// classifyOrigin accepts both separators on every OS: filepath.ToSlash
// would be a no-op for Windows paths seen on Unix (and in tests).
func classifyOrigin(path string) lopper.Origin {
	p := strings.ReplaceAll(path, `\`, "/") + "/"
	for _, m := range originMarkers {
		if strings.Contains(p, m.fragment) {
			return m.origin
		}
	}
	return lopper.OriginManual
}
