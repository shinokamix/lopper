package discovery

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/shinokamix/lopper/internal/gitx"
	"github.com/shinokamix/lopper/internal/lopper"
)

// orphanWorktree describes a worktree by its directory alone. With the
// admin directory gone, its branch and HEAD are unknown.
func orphanWorktree(dir, commonDir string) lopper.Worktree {
	path := filepath.Clean(dir)
	return lopper.Worktree{
		ID:    lopper.ID(path),
		Path:  path,
		Repo:  lopper.Repo{Path: repoPath(commonDir)},
		State: lopper.StateOrphaned,
	}
}

// unconfirmedWorktree describes a worktree by its directory and its admin
// directory, which holds its checked-out branch or commit and its lock. It
// assumes a lock unless the lock file definitely does not exist, because a
// lock is the user's explicit wish to keep the worktree.
func unconfirmedWorktree(dir string, repo lopper.Repo, admin, why string) lopper.Worktree {
	path := filepath.Clean(dir)
	wt := lopper.Worktree{
		ID:     lopper.ID(path),
		Path:   path,
		Repo:   repo,
		Locked: !isGone(filepath.Join(admin, "locked")),
		State:  lopper.StateUnconfirmed,
		Reason: why,
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

// movedWorktree returns stale, which git lists as missing, at dir, where it
// lives now.
func movedWorktree(stale lopper.Worktree, dir string) lopper.Worktree {
	wt := stale
	wt.ID, wt.Path = lopper.ID(dir), dir
	wt.State, wt.MovedFrom = lopper.StateMoved, stale.Path
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

// hasLinkedWorktrees reports, without running git, whether the repository
// can have linked worktrees. Almost none do, so this saves a git process per
// repository.
func hasLinkedWorktrees(gitDir string) bool {
	f, err := os.Open(filepath.Join(gitDir, "worktrees"))
	if err != nil {
		return false
	}
	defer f.Close()
	names, _ := f.Readdirnames(1)
	return len(names) > 0
}

// listRepo emits the linked worktrees that the repository at gitDir lists
// and returns the repository, or a zero Repo if its worktrees directory is
// missing or empty. It fails only when git does, and then emits nothing.
func listRepo(ctx context.Context, git gitx.Runner, gitDir string, emit func(lopper.Worktree)) (lopper.Repo, error) {
	if !hasLinkedWorktrees(gitDir) {
		return lopper.Repo{}, nil
	}
	dir := repoPath(gitDir)
	if dir == gitDir {
		git = gitx.OwnWorkTree{Runner: git} // its checkout, if any, may be gone
	}
	entries, err := gitx.ListWorktrees(ctx, git, dir)
	if err != nil {
		return lopper.Repo{}, err
	}
	// Worktrees found on disk that git left out need the base branch too.
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
		})
	}
	return repo, nil
}
