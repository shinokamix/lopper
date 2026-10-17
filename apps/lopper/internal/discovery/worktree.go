package discovery

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/shinokamix/lopper/apps/lopper/internal/gitx"
	"github.com/shinokamix/lopper/apps/lopper/internal/lopper"
)

func orphanWorktree(dir, commonDir string) lopper.Worktree {
	path := filepath.Clean(dir)
	return lopper.Worktree{
		ID:    lopper.ID(path),
		Path:  path,
		Repo:  lopper.Repo{Path: repoPath(commonDir)},
		State: lopper.StateOrphaned,
	}
}

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

func movedWorktree(stale lopper.Worktree, dir string) lopper.Worktree {
	wt := stale
	wt.ID, wt.Path = lopper.ID(dir), dir
	wt.State, wt.MovedFrom = lopper.StateMoved, stale.Path
	return wt
}

func repoPath(gitDir string) string {
	if filepath.Base(gitDir) == ".git" {
		return filepath.Dir(gitDir)
	}
	return gitDir
}

func hasLinkedWorktrees(gitDir string) bool {
	f, err := os.Open(filepath.Join(gitDir, "worktrees"))
	if err != nil {
		return false
	}
	defer f.Close()
	names, _ := f.Readdirnames(1)
	return len(names) > 0
}

func listRepo(ctx context.Context, git gitx.Runner, gitDir string, emit func(lopper.Worktree)) (lopper.Repo, error) {
	if !hasLinkedWorktrees(gitDir) {
		return lopper.Repo{}, nil
	}
	dir := repoPath(gitDir)
	if dir == gitDir {
		git = gitx.OwnWorkTree{Runner: git}
	}
	entries, err := gitx.ListWorktrees(ctx, git, dir)
	if err != nil {
		return lopper.Repo{}, err
	}
	repo := lopper.Repo{
		Path:          dir,
		DefaultBranch: gitx.DefaultBranch(ctx, git, dir),
	}
	if len(entries) == 0 {
		return repo, nil
	}
	linked := entries[1:]
	for _, e := range linked {
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
