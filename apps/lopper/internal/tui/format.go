package tui

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/x/ansi"

	"github.com/shinokamix/lopper/apps/lopper/internal/lopper"
	"github.com/shinokamix/lopper/apps/lopper/internal/verdict"
)

// facts is what a row shows about its worktree. "merged" keeps its color
// whether or not the row is safe, as on GitHub. Work that would be lost
// comes first, in its own color.
func facts(r *row) []verdict.Note {
	if !r.checked {
		return []verdict.Note{{Text: "checking…"}}
	}
	return verdict.Notes(r.worktree, r.facts)
}

const noteSep = " · "

// notesWidth is how many cells notes take in one line.
func notesWidth(notes []verdict.Note) int {
	w := 0
	for i, n := range notes {
		if i > 0 {
			w += len(noteSep) - 1 // the dot is one cell but two bytes
		}
		w += ansi.StringWidth(n.Text)
	}
	return w
}

// fitNotes returns the leading notes that fit in w cells and how many are
// left out, which the row counts as "+N" rather than drop silently. If not
// even the first note fits whole, fitNotes cuts it short rather than leave a
// bare count.
func fitNotes(notes []verdict.Note, w int) (shown []verdict.Note, hidden int) {
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
	if room < 4 { // too short to recognize, so a count says more
		return nil, len(notes)
	}
	first.Text = ansi.Truncate(first.Text, room, "…")
	return []verdict.Note{first}, rest
}

// more is the count of notes left out, shown after the last one.
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
// name. aliases are ordered longest directory first.
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

// fitPath shortens p to at most w cells by replacing whole directories in
// the middle with "…". It keeps the first directory, often ~ or tmp, and as
// many of the last ones as fit, as in tmp/…/lab/outside/c3.
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
	// Not even the last directory fits, so keep its end.
	return ansi.TruncateLeft(p, ansi.StringWidth(p)-w+1, "…")
}

// fit truncates or pads s to exactly w terminal cells.
func fit(s string, w int) string {
	s = ansi.Truncate(s, w, "…")
	return s + strings.Repeat(" ", max(w-ansi.StringWidth(s), 0))
}

// formatBytes formats a size in SI units like "1.4 GB", as macOS Finder
// does.
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
