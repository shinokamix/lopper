package tui

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/x/ansi"

	"github.com/shinokamix/lopper/internal/lopper"
)

// facts is what a row shows about its worktree. "merged" gets its color
// only on a safe row: users delete merged rows without looking closer.
func facts(r *row) []lopper.Note {
	if !r.checked {
		return []lopper.Note{{Text: "checking…"}}
	}
	notes := lopper.Notes(r.worktree, r.facts)
	for i, n := range notes {
		if n.Kind == lopper.NoteMerged && !r.safe {
			notes[i].Kind = lopper.NotePlain
		}
	}
	return notes
}

// noteSep separates the notes of a row.
const noteSep = " · "

// notesWidth is how many cells notes take in one line.
func notesWidth(notes []lopper.Note) int {
	w := 0
	for i, n := range notes {
		if i > 0 {
			w += len(noteSep) - 1 // the dot is one cell but two bytes
		}
		w += ansi.StringWidth(n.Text)
	}
	return w
}

// fitNotes returns the leading notes that fit in w cells, and how many
// are left out: those are counted as "+N", never dropped silently. When
// not even the first fits whole, it is cut short rather than leaving a
// bare count.
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
	if room < 4 { // too short to recognize: a count says more
		return nil, len(notes)
	}
	first.Text = ansi.Truncate(first.Text, room, "…")
	return []lopper.Note{first}, rest
}

// more counts notes left out, to follow the last one shown.
func more(n int) string { return fmt.Sprintf(" +%d", n) }

// branchName is the branch, or the short commit of a detached HEAD.
func branchName(wt lopper.Worktree) string {
	if wt.Branch != "" {
		return wt.Branch
	}
	if len(wt.Head) >= 7 {
		return "detached @" + wt.Head[:7]
	}
	return "(detached)"
}

// alias is a short name for a directory that paths often start with.
type alias struct{ dir, name string }

// abbrev replaces the directory of the first matching alias with its
// name; aliases are ordered longest directory first.
func abbrev(p string, aliases []alias) string {
	for _, a := range aliases {
		rel, err := filepath.Rel(a.dir, p)
		if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return filepath.Join(a.name, rel)
		}
	}
	return p
}

// repoName names a repository by its directory, skipping the
// conventional names of bare repositories: ~/code/app/.bare and
// ~/code/app.git are both "app".
func repoName(path string) string {
	name := filepath.Base(path)
	if name == ".bare" || name == ".git" {
		name = filepath.Base(filepath.Dir(path))
	}
	return strings.TrimSuffix(name, ".git")
}

// fitPath shortens p to at most w cells by replacing whole directories
// in the middle with "…", keeping the first one (often ~ or tmp) and as
// many of the last ones as fit: tmp/…/lab/outside/c3.
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
	// Not even the last directory fits: keep its end.
	return ansi.TruncateLeft(p, ansi.StringWidth(p)-w+1, "…")
}

// fit truncates or pads s to exactly w terminal cells.
func fit(s string, w int) string {
	s = ansi.Truncate(s, w, "…")
	return s + strings.Repeat(" ", max(w-ansi.StringWidth(s), 0))
}

// formatBytes formats a size in SI units like "1.4 GB" (as macOS Finder
// does).
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
