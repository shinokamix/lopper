// Package lopper holds the core domain types shared by every other package.
// It must not import anything from this module.
package lopper

import "fmt"

// ID uniquely identifies a worktree: its absolute, cleaned path.
type ID string

// Repo is a main repository that owns one or more linked worktrees.
type Repo struct {
	Path          string // main worktree path
	DefaultBranch string // e.g. "main"; empty if unknown
}

// Origin describes which tool most likely created a worktree.
type Origin string

const (
	OriginManual     Origin = "manual"
	OriginClaudeCode Origin = "claude-code"
	OriginCodex      Origin = "codex"
	OriginConductor  Origin = "conductor"
)

// Worktree is a linked worktree found on disk.
type Worktree struct {
	ID     ID
	Path   string
	Repo   Repo
	Branch string // empty when HEAD is detached
	Head   string // commit SHA
	Locked bool
	State  State
	// MovedFrom is where git still expects a StateMoved worktree.
	MovedFrom string
	// Reason tells why a StateUnconfirmed worktree could not be confirmed.
	Reason string
	Origin Origin
}

// State tells whether git tracks a worktree where it is, and if not, what
// became of it. A worktree is in exactly one state.
type State int

const (
	// StateTracked: git tracks it where it is.
	StateTracked State = iota
	// StateGone: git reports its directory as missing; only git's record
	// of it is left.
	StateGone
	// StateOrphaned: the directory is still there, but its repository no
	// longer tracks it (deleted, or the worktree was pruned), so git cannot
	// inspect it. Repo.Path is where the repository used to be.
	StateOrphaned
	// StateMoved: the directory was moved here by hand from MovedFrom,
	// where git still expects it; `git worktree repair` run inside it
	// relinks them.
	StateMoved
	// StateUnconfirmed: the directory's .git file makes it a linked
	// worktree of Repo, but the repository could not confirm that it still
	// tracks it here; Reason says why. git may or may not still work inside
	// it.
	StateUnconfirmed
)

// Facts are observations about a worktree. Inspect fills them in
// progressively; a nil pointer means "not known". A fact that could not
// be observed stays nil and the cause is recorded in Errors.
type Facts struct {
	Dirty          *int       `json:"dirty,omitempty"`           // status entries; an untracked directory counts as one
	UncheckedFiles *int       `json:"unchecked_files,omitempty"` // files whose index flags prevent checking for edits
	Unpushed       *int       `json:"unpushed,omitempty"`        // commits neither on a remote nor in base
	Merged         *MergeKind `json:"merged,omitempty"`          // how (if at all) the work reached the base branch
	SizeBytes      *int64     `json:"size_bytes,omitempty"`
	Errors         []string   `json:"errors,omitempty"` // why facts are missing, e.g. "could not read status: ..."
}

// MergeKind tells how a branch was integrated into the base branch.
type MergeKind string

const (
	NotMerged    MergeKind = "none"
	MergedFF     MergeKind = "ancestor" // HEAD is an ancestor of base
	MergedSquash MergeKind = "content"  // changes found after squash, rebase, or cherry-pick
)

// Note is one fact about a worktree in words, as the list and the CLI
// show it.
type Note struct {
	Text string
	Kind NoteKind
}

// NoteKind tells what a note is about, for the list to color it.
type NoteKind int

const (
	NotePlain  NoteKind = iota
	NoteWork            // work or unchecked files that deletion puts at risk
	NoteMerged          // the work is in the base branch
)

// Notes describes a worktree by its facts, most pressing first: work
// that deleting it would lose, what git cannot tell about it, then how far
// the work got. It describes and never decides: whether the worktree is
// safe to delete is verdict's call.
func Notes(wt Worktree, f Facts) []Note {
	var out []Note
	add := func(kind NoteKind, format string, a ...any) {
		out = append(out, Note{fmt.Sprintf(format, a...), kind})
	}
	merged := f.Merged != nil && *f.Merged != NotMerged
	if wt.Locked {
		add(NoteWork, "locked")
	}
	if f.Dirty != nil && *f.Dirty > 0 {
		add(NoteWork, "%d uncommitted", *f.Dirty)
	}
	if f.UncheckedFiles != nil && *f.UncheckedFiles > 0 {
		add(NoteWork, "%d unchecked", *f.UncheckedFiles)
	}
	if f.Unpushed != nil && *f.Unpushed > 0 && !merged {
		add(NoteWork, "%d unpushed", *f.Unpushed)
	}
	switch wt.State {
	case StateTracked:
	case StateGone:
		add(NotePlain, "folder gone")
	case StateOrphaned:
		add(NotePlain, "not tracked by git")
	case StateMoved:
		add(NotePlain, "moved by hand")
	case StateUnconfirmed:
		add(NotePlain, "not confirmed by git")
	}
	if wt.State != StateGone && wt.State != StateOrphaned && (f.Dirty == nil || f.UncheckedFiles == nil || f.Unpushed == nil || f.Merged == nil) {
		add(NotePlain, "couldn't check")
	}
	switch {
	case merged:
		add(NoteMerged, "merged")
	case f.Merged != nil:
		add(NotePlain, "not merged")
	}
	return out
}
