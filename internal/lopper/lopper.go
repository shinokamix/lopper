// Package lopper holds the core domain types shared by every other package.
// It must not import anything from this module.
package lopper

// ID uniquely identifies a worktree: its absolute, cleaned path.
type ID string

// Repo is a main repository that owns one or more linked worktrees.
type Repo struct {
	Path          string // main worktree path
	DefaultBranch string // e.g. "main"; empty if unknown
}

// Origin describes which tool most likely created a worktree.
type Origin string

// Origins lopper tells apart by where a worktree lies; OriginManual is any
// other.
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
	// StateTracked means git tracks it where it is.
	StateTracked State = iota
	// StateGone means git reports its directory as missing; only git's record
	// of it is left.
	StateGone
	// StateOrphaned means the directory is still there, but its repository no
	// longer tracks it (deleted, or the worktree was pruned), so git cannot
	// inspect it. Repo.Path is where the repository used to be.
	StateOrphaned
	// StateMoved means the directory was moved here by hand from MovedFrom,
	// where git still expects it; `git worktree repair` run inside it
	// relinks them.
	StateMoved
	// StateUnconfirmed means the directory's .git file makes it a linked
	// worktree of Repo, but the repository could not confirm that it still
	// tracks it here; Reason says why. git may or may not still work inside
	// it.
	StateUnconfirmed
)

// Facts are observations about a worktree. Inspect fills them in
// progressively; a nil pointer means "not known". A fact that could not
// be observed stays nil and the cause is recorded in Errors.
type Facts struct {
	Dirty          *int       // status entries; an untracked directory counts as one
	UncheckedFiles *int       // files whose index flags prevent checking for edits
	Unpushed       *int       // commits neither on a remote nor in base
	Merged         *MergeKind // how (if at all) the work reached the base branch
	SizeBytes      *int64
	Errors         []string // why facts are missing, e.g. "could not read status: ..."
}

// MergeKind tells how a branch was integrated into the base branch.
type MergeKind string

// How a branch reached the base branch, if it did.
const (
	NotMerged    MergeKind = "none"
	MergedFF     MergeKind = "ancestor" // HEAD is an ancestor of base
	MergedSquash MergeKind = "content"  // changes found after squash, rebase, or cherry-pick
)
