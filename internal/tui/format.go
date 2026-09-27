package tui

import (
	"fmt"

	"github.com/shinokamix/lopper/internal/lopper"
)

// badge renders a verdict level as a colored dot and label.
func badge(t theme, l lopper.Level) string {
	return t.level[l].Render(fmt.Sprintf("● %-7s", l))
}

// formatBytes formats a size in SI units like "1.4 GB" (as macOS Finder
// does); nil renders as a placeholder.
func formatBytes(n *int64) string {
	if n == nil {
		return "…"
	}
	const unit = 1000
	if *n < unit {
		return fmt.Sprintf("%d B", *n)
	}
	div, exp := int64(unit), 0
	for v := *n / unit; v >= unit; v /= unit {
		div *= unit
		exp++
	}
	prefixes := [...]string{"k", "M", "G", "T", "P", "E"}
	return fmt.Sprintf("%.1f %sB", float64(*n)/float64(div), prefixes[exp])
}
