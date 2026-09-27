package tui

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/x/ansi"

	"github.com/shinokamix/lopper/internal/lopper"
)

// state is how the list presents a verdict: a fact the user can check
// instead of a judgement. It is derived from the verdict level, so a row
// shows merged exactly when the verdict calls it safe.
type state int

const (
	stateChecking  state = iota // no verdict yet
	stateMerged                 // safe: the work is in the base branch
	stateNotMerged              // review only because the work is not merged
	stateLocalWork              // keep: work exists only in this worktree
	stateUnknown                // review for any other reason: lopper cannot tell
)

// states lists the settled states in display order.
var states = [...]state{stateMerged, stateNotMerged, stateLocalWork, stateUnknown}

func (s state) String() string {
	switch s {
	case stateMerged:
		return "merged"
	case stateNotMerged:
		return "not merged"
	case stateLocalWork:
		return "local work"
	case stateUnknown:
		return "unknown"
	default:
		return "checking"
	}
}

// classify returns a row's state and the facts behind it, e.g.
// "3 uncommitted · 2 unpushed".
func classify(r *row) (state, string) {
	v := r.verdict
	// Only the reasons that decided the level are shown. Among review
	// reasons, not being merged is a known fact; any other means lopper
	// cannot vouch for the worktree.
	var decisive, unsure []string
	for _, reason := range v.Reasons {
		if reason.Level != v.Level {
			continue
		}
		label := reasonLabel(reason, r.facts)
		decisive = append(decisive, label)
		if reason.Rule != "not-merged" {
			unsure = append(unsure, label)
		}
	}
	switch v.Level {
	case lopper.LevelSafe:
		return stateMerged, strings.Join(decisive, " · ")
	case lopper.LevelKeep:
		return stateLocalWork, strings.Join(decisive, " · ")
	case lopper.LevelReview:
		if len(unsure) == 0 {
			return stateNotMerged, strings.Join(decisive, " · ")
		}
		return stateUnknown, strings.Join(unsure, " · ")
	default:
		return stateChecking, "checking…"
	}
}

// reasonLabel is the few-word form of a verdict reason.
func reasonLabel(r lopper.Reason, f lopper.Facts) string {
	switch r.Rule {
	case "prunable":
		return "folder gone"
	case "orphaned":
		return "not tracked by git"
	case "moved":
		return "moved by hand"
	case "unconfirmed":
		return "not confirmed by git"
	case "locked":
		return "locked"
	case "incomplete":
		return "couldn't check"
	case "dirty":
		if f.Dirty != nil {
			return fmt.Sprintf("%d uncommitted", *f.Dirty)
		}
	case "unpushed":
		if f.Unpushed != nil {
			return fmt.Sprintf("%d unpushed", *f.Unpushed)
		}
	case "merged":
		return "merged"
	case "not-merged":
		return "not merged"
	}
	return r.Rule
}

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
