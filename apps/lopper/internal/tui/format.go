package tui

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/x/ansi"

	"github.com/shinokamix/lopper/apps/lopper/internal/lopper"
)

func facts(r *row) []lopper.Note {
	if !r.checked {
		return []lopper.Note{{Text: "checking…"}}
	}
	return r.verdict.Notes
}

const noteSep = " · "

func notesWidth(notes []lopper.Note) int {
	w := 0
	for i, n := range notes {
		if i > 0 {
			w += len(noteSep) - 1
		}
		w += ansi.StringWidth(n.Text)
	}
	return w
}

func fitNotes(notes []lopper.Note, w int) (shown []lopper.Note, hidden int) {
	for k := len(notes); k > 0; k-- {
		need := notesWidth(notes[:k])
		if k < len(notes) {
			need += ansi.StringWidth(more(len(notes) - k))
		}
		if need <= w {
			return notes[:k], len(notes) - k
		}
	}
	if len(notes) == 0 {
		return nil, 0
	}
	first, rest := notes[0], len(notes)-1
	room := w
	if rest > 0 {
		room -= ansi.StringWidth(more(rest))
	}
	if room < 4 {
		return nil, len(notes)
	}
	first.Text = ansi.Truncate(first.Text, room, "…")
	return []lopper.Note{first}, rest
}

func more(n int) string { return fmt.Sprintf(" +%d", n) }

func branchName(wt lopper.Worktree) string {
	if wt.Branch != "" {
		return wt.Branch
	}
	if len(wt.Head) >= 7 {
		return "detached @" + wt.Head[:7]
	}
	return "(detached)"
}

type alias struct{ dir, name string }

func abbrev(p string, aliases []alias) string {
	for _, a := range aliases {
		rel, err := filepath.Rel(a.dir, p)
		if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return filepath.Join(a.name, rel)
		}
	}
	return p
}

func repoName(path string) string {
	name := filepath.Base(path)
	if name == ".bare" || name == ".git" {
		name = filepath.Base(filepath.Dir(path))
	}
	return strings.TrimSuffix(name, ".git")
}

func fitPath(p string, w int) string {
	if ansi.StringWidth(p) <= w {
		return p
	}
	sep := string(filepath.Separator)
	parts := strings.Split(p, sep)
	for _, head := range []string{parts[0] + sep + "…", "…"} {
		best := ""
		for k := 1; k < len(parts)-1; k++ {
			c := head + sep + strings.Join(parts[len(parts)-k:], sep)
			if ansi.StringWidth(c) > w {
				break
			}
			best = c
		}
		if best != "" {
			return best
		}
	}
	return ansi.TruncateLeft(p, ansi.StringWidth(p)-w+1, "…")
}

func fit(s string, w int) string {
	s = ansi.Truncate(s, w, "…")
	return s + strings.Repeat(" ", max(w-ansi.StringWidth(s), 0))
}

func formatBytes(n int64) string {
	const unit = 1000
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit; v /= unit {
		div *= unit
		exp++
	}
	prefixes := [...]string{"k", "M", "G", "T", "P", "E"}
	return fmt.Sprintf("%.1f %sB", float64(n)/float64(div), prefixes[exp])
}
