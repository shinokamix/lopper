package discovery

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/shinokamix/lopper/internal/gitx"
	"github.com/shinokamix/lopper/internal/lopper"
)

// orphanWorktree describes a worktree from its directory alone: with the
// admin directory gone, its branch and HEAD are unknown.
func orphanWorktree(dir, commonDir string) lopper.Worktree {
	path := filepath.Clean(dir)
	return lopper.Worktree{
		ID:     lopper.ID(path),
		Path:   path,
		Repo:   lopper.Repo{Path: repoPath(commonDir)},
		State:  lopper.StateOrphaned,
		Origin: classifyOrigin(path),
	}
}

// unconfirmedWorktree describes a worktree from its directory and its
// admin directory, which tells the branch or commit it has checked out and
// whether it is locked. A lock that cannot be ruled out is assumed: it is
// the user's explicit wish to keep the worktree.
func unconfirmedWorktree(dir string, repo lopper.Repo, admin, why string) lopper.Worktree {
	path := filepath.Clean(dir)
	wt := lopper.Worktree{
		ID:     lopper.ID(path),
		Path:   path,
		Repo:   repo,
		Locked: !isGone(filepath.Join(admin, "locked")),
		State:  lopper.StateUnconfirmed,
		Reason: why,
		Origin: classifyOrigin(path),
	}
	if head, err := os.ReadFile(filepath.Join(admin, "HEAD")); err == nil {
		ref := strings.TrimSpace(string(head))
		if branch, ok := strings.CutPrefix(ref, "ref: refs/heads/"); ok {
			wt.Branch = branch
		} else if !strings.HasPrefix(ref, "ref:") {
			wt.Head = ref
		}
	}
	return wt
}

// movedWorktree is a worktree that git lists as missing because it now
// lives in dir: what git knows about it, at the path it has now.
func movedWorktree(stale lopper.Worktree, dir string) lopper.Worktree {
	wt := stale
	wt.ID, wt.Path = lopper.ID(dir), dir
	wt.State, wt.MovedFrom = lopper.StateMoved, stale.Path
	wt.Origin = classifyOrigin(dir)
	return wt
}

// repoPath is the main worktree of a repository, or the git directory
// itself for a bare repository.
func repoPath(gitDir string) string {
	if filepath.Base(gitDir) == ".git" {
		return filepath.Dir(gitDir)
	}
	return gitDir
}

// hasLinkedWorktrees tells, without spawning git, whether the repository
// can have linked worktrees at all. Almost none do, so this saves one
// git process per repository on disk.
func hasLinkedWorktrees(gitDir string) bool {
	f, err := os.Open(filepath.Join(gitDir, "worktrees"))
	if err != nil {
		return false
	}
	defer f.Close()
	names, _ := f.Readdirnames(1)
	return len(names) > 0
}

// listRepo emits the linked worktrees the repository at gitDir lists,
// and returns the repository, unless it has no worktrees directory to
// list. It fails only when git does: then no worktree is emitted.
func listRepo(ctx context.Context, git gitx.Runner, gitDir string, emit func(lopper.Worktree)) (lopper.Repo, error) {
	if !hasLinkedWorktrees(gitDir) {
		return lopper.Repo{}, nil
	}
	// Run git from the main worktree when there is one.
	dir := repoPath(gitDir)
	if dir == gitDir {
		git = gitx.OwnWorkTree{Runner: git} // its checkout, if any, may be gone
	}
	entries, err := gitx.ListWorktrees(ctx, git, dir)
	if err != nil {
		return lopper.Repo{}, err
	}
	// Even with no linked worktree listed, the base branch is needed for
	// the ones found on disk that git left out.
	repo := lopper.Repo{
		Path:          dir,
		DefaultBranch: gitx.DefaultBranch(ctx, git, dir),
	}
	if len(entries) == 0 {
		return repo, nil
	}
	for _, e := range entries[1:] { // entries[0] is the main worktree
		if e.Bare {
			continue
		}
		path := filepath.Clean(e.Path)
		state := lopper.StateTracked
		if e.Prunable {
			state = lopper.StateGone
		}
		emit(lopper.Worktree{
			ID:     lopper.ID(path),
			Path:   path,
			Repo:   repo,
			Branch: e.Branch,
			Head:   e.Head,
			Locked: e.Locked,
			State:  state,
			Origin: classifyOrigin(path),
		})
	}
	return repo, nil
}
