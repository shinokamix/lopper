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

const (
	OriginManual     Origin = "manual"
	OriginClaudeCode Origin = "claude-code"
	OriginCodex      Origin = "codex"
	OriginConductor  Origin = "conductor"
)

// Worktree is a linked worktree found on disk.
type Worktree struct {
	ID       ID
	Path     string
	Repo     Repo
	Branch   string // empty when HEAD is detached
	Head     string // commit SHA
	Locked   bool
	Prunable bool // git reports the directory as missing
	Origin   Origin
}

// Facts are observations about a worktree. Inspect fills them in
// progressively; a nil pointer means "not known". A fact that could not
// be observed stays nil and the cause is recorded in Errors.
type Facts struct {
	Dirty     *int       `json:"dirty,omitempty"`    // status entries; an untracked directory counts as one
	Unpushed  *int       `json:"unpushed,omitempty"` // commits neither on a remote nor in base
	Merged    *MergeKind `json:"merged,omitempty"`   // how (if at all) the work reached the base branch
	SizeBytes *int64     `json:"size_bytes,omitempty"`
	Errors    []string   `json:"errors,omitempty"` // why facts are missing, e.g. "could not read status: ..."
}

// MergeKind tells how a branch was integrated into the base branch.
type MergeKind string

const (
	NotMerged    MergeKind = "none"
	MergedFF     MergeKind = "ancestor" // HEAD is an ancestor of base
	MergedSquash MergeKind = "patch-id" // all patches found in base
)

// Level is the overall recommendation for a worktree.
//
// The numeric order of the constants IS the severity order:
// Unknown < Safe < Review < Keep. verdict.Evaluate relies on it to pick
// the strictest reason, so never reorder them.
type Level int

const (
	LevelUnknown Level = iota
	LevelSafe          // can be removed without losing work
	LevelReview        // probably removable, a human should look
	LevelKeep          // removing would lose work or break something
)

func (l Level) String() string {
	switch l {
	case LevelSafe:
		return "safe"
	case LevelReview:
		return "review"
	case LevelKeep:
		return "keep"
	default:
		return "unknown"
	}
}

func (l Level) MarshalText() ([]byte, error) { return []byte(l.String()), nil }

// Reason is a single human-readable argument behind a verdict.
type Reason struct {
	Rule    string `json:"rule"` // stable rule id, e.g. "unpushed"
	Level   Level  `json:"level"`
	Message string `json:"message"`
}

// Verdict is the result of evaluating all rules against Facts.
type Verdict struct {
	Level   Level
	Reasons []Reason
}
