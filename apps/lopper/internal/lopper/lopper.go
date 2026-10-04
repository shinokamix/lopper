// Package lopper holds the types every other package shares. It must not
// import anything from this module.
package lopper

// ID identifies a worktree by its absolute, cleaned path.
type ID string

// Repo is a main repository that owns linked worktrees.
type Repo struct {
	Path          string // the main worktree
	DefaultBranch string // empty if unknown
}

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
	// Reason says why git could not confirm a StateUnconfirmed worktree.
	Reason string
}

// State says whether git tracks a worktree at its path, and if not, what
// happened to it.
type State int

const (
	// StateTracked means git tracks the worktree at its path.
	StateTracked State = iota
	// StateGone means the directory is gone and only git's record of it is
	// left.
	StateGone
	// StateOrphaned means the directory exists but its repository no longer
	// tracks it, because the repository was deleted or the worktree pruned.
	// git cannot inspect it. Repo.Path is where the repository was.
	StateOrphaned
	// StateMoved means someone moved the directory here from MovedFrom, where
	// git still expects it. `git worktree repair` run inside it relinks them.
	StateMoved
	// StateUnconfirmed means the .git file names Repo, but the repository
	// could not confirm that it tracks the directory here. Reason says why.
	// git may still work inside it.
	StateUnconfirmed
)

// Facts are what inspect observed about a worktree, in stages. A nil field
// is unknown, and Errors says why when an observation failed.
type Facts struct {
	Dirty          *int       // status entries; an untracked directory counts as one
	UncheckedFiles *int       // files whose index flags hide edits from status
	Unpushed       *int       // commits neither on a remote nor in base
	Merged         *MergeKind // how the work reached the base branch, if it did
	SizeBytes      *int64
	Errors         []string
}

// MergeKind says how a branch reached the base branch.
type MergeKind string

// The kinds of merge.
const (
	NotMerged    MergeKind = "none"
	MergedFF     MergeKind = "ancestor" // HEAD is an ancestor of base
	MergedSquash MergeKind = "content"  // base has the changes after a squash, rebase or cherry-pick
)
