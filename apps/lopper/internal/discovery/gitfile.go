package discovery

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

type gitFile struct {
	admin     string
	commonDir string
	orphaned  bool
	movedFrom string
	repoGone  bool
	damage    string
}

// readGitFile resolves a .git file, which reads "gitdir: <dir>", to the
// common git directory of its repository. For a linked worktree <dir> is its
// admin directory inside the main repository. For a submodule or a
// --separate-git-dir checkout it is the whole git directory.
func readGitFile(dotGit string) (gitFile, bool) {
	content, err := os.ReadFile(dotGit)
	if err != nil {
		return gitFile{}, false
	}
	admin, ok := strings.CutPrefix(strings.TrimSpace(string(content)), "gitdir:")
	if !ok {
		return gitFile{}, false
	}
	admin = strings.TrimSpace(admin)
	if !filepath.IsAbs(admin) {
		admin = filepath.Join(filepath.Dir(dotGit), admin)
	}
	admin = filepath.Clean(admin)

	if _, err := os.Stat(admin); errors.Is(err, fs.ErrNotExist) {
		return orphanGitFile(admin)
	}

	// `git worktree add` always writes commondir, and git finds the common
	// directory through it. An admin directory without one is a whole
	// repository even at worktrees/<name>, such as a submodule's or one moved
	// with --separate-git-dir. Otherwise a directory in a repository's
	// worktrees directory belongs to a worktree that lost its commondir.
	c, err := os.ReadFile(filepath.Join(admin, "commondir"))
	if err != nil {
		if isGitDir(admin) {
			return gitFile{admin: admin, commonDir: admin}, true
		}
		if holder := holdingRepo(admin); holder != "" && errors.Is(err, fs.ErrNotExist) {
			return gitFile{admin: admin, commonDir: holder, damage: "commondir is missing from " + admin}, true
		}
		return gitFile{}, false
	}
	common := strings.TrimSpace(string(c))
	if !filepath.IsAbs(common) {
		common = filepath.Join(admin, common)
	}
	common = filepath.Clean(common)

	// git fails in a worktree whose common directory it cannot open, and in
	// every other worktree of that repository too.
	if _, err := os.Stat(common); err != nil || !isGitDir(common) {
		why := common + " is not a git repository"
		if errors.Is(err, fs.ErrNotExist) {
			why = common + " is gone"
		}
		if holder := holdingRepo(admin); holder != "" && holder != realPath(common) {
			return gitFile{admin: admin, commonDir: holder, damage: "commondir names " + why}, true
		}
		return gitFile{admin: admin, commonDir: common, repoGone: true, damage: "its git directory " + why}, true
	}
	// The admin directory may now belong to another worktree, because git
	// reuses a name that a prune freed. Or someone moved this worktree away
	// from where git expects it. Either way the repository no longer tracks
	// this directory.
	intact, movedFrom, damage := backLink(admin, dotGit)
	if damage == "" && isGone(filepath.Join(admin, "HEAD")) {
		damage = "HEAD is missing from " + admin
	}
	return gitFile{admin: admin, commonDir: common, orphaned: !intact, movedFrom: movedFrom, damage: damage}, true
}

func holdingRepo(admin string) string {
	parent := filepath.Dir(admin)
	if filepath.Base(parent) != "worktrees" || !isGitDir(filepath.Dir(parent)) {
		return ""
	}
	return realPath(filepath.Dir(parent))
}

// backLink checks the admin directory's gitdir file, the link that `git
// worktree repair` fixes. It reports whether the link names dotGit and, if
// it names a .git file that is gone, the directory where git still expects
// the worktree. A missing or empty link is damage, not proof that the
// repository dropped the worktree. git still works in it, but `git worktree
// list` leaves it out and `git worktree prune` deletes its admin directory.
// A link unreadable for any other reason counts as intact.
func backLink(admin, dotGit string) (intact bool, movedFrom, damage string) {
	content, err := os.ReadFile(filepath.Join(admin, "gitdir"))
	if errors.Is(err, fs.ErrNotExist) {
		return true, "", "gitdir is missing from " + admin
	}
	if err != nil {
		return true, "", ""
	}
	target := strings.TrimSpace(string(content))
	if target == "" {
		return true, "", "gitdir is empty in " + admin
	}
	if !filepath.IsAbs(target) {
		target = filepath.Join(admin, target) // worktree.useRelativePaths
	}
	target = filepath.Clean(target)
	want, err := os.Stat(target)
	if errors.Is(err, fs.ErrNotExist) {
		return false, filepath.Dir(target), ""
	}
	if err != nil {
		return true, "", ""
	}
	got, err := os.Stat(dotGit)
	// SameFile, not path equality, because of symlinks and of case-insensitive
	// file systems on Windows and macOS.
	return err != nil || os.SameFile(want, got), "", ""
}

// orphanGitFile explains a .git file whose admin directory is gone. It must
// not mistake a submodule for a worktree. A submodule checked out at
// worktrees/<name> keeps its git directory at
// <super>/.git/modules/worktrees/<name>, which looks the same.
func orphanGitFile(admin string) (gitFile, bool) {
	common := filepath.Dir(filepath.Dir(admin))
	if filepath.Base(filepath.Dir(admin)) != "worktrees" {
		return gitFile{}, false
	}
	info, err := os.Stat(common)
	if errors.Is(err, fs.ErrNotExist) {
		if insideGitDir(common) {
			return gitFile{}, false
		}
		return gitFile{admin: admin, commonDir: common, orphaned: true, repoGone: true}, true
	}
	// <super>/.git/modules is not a repository, but a submodule's own
	// repository at <super>/.git/modules/<name> is, and may have worktrees.
	if err != nil || !info.IsDir() || !isGitDir(common) {
		return gitFile{}, false
	}
	return gitFile{admin: admin, commonDir: common, orphaned: true}, true
}

func insideGitDir(path string) bool {
	for dir := filepath.Dir(path); ; dir = filepath.Dir(dir) {
		if filepath.Base(dir) == ".git" || isGitDir(dir) {
			return true
		}
		if filepath.Dir(dir) == dir {
			return false
		}
	}
}

// isGitDir mirrors is_git_directory in git's setup.c. A repository has
// HEAD, objects and refs.
func isGitDir(dir string) bool {
	for _, name := range []string{"HEAD", "objects", "refs"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			return false
		}
	}
	return true
}
