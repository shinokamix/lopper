package discovery

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// gitFile is what a .git file tells about its repository.
type gitFile struct {
	admin     string // the git directory the .git file names
	commonDir string // the repository's common git directory; admin for a whole repository
	orphaned  bool   // commonDir no longer tracks this worktree here
	movedFrom string // where commonDir expects this worktree instead
	repoGone  bool   // commonDir cannot be listed: it is gone, or no repository
	damage    string // why the metadata cannot confirm this worktree; "" if intact
}

// readGitFile resolves a .git file ("gitdir: <dir>") to the common git
// directory of the repository it belongs to. For a linked worktree <dir>
// is its admin directory inside the main repository; for a submodule or
// a --separate-git-dir checkout it is the whole git directory.
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

	// Following the paths git wrote is the point: they may lead anywhere.
	// Only a definite "does not exist" makes an orphan: a permission error
	// says nothing about the repository.
	if _, err := os.Stat(admin); errors.Is(err, fs.ErrNotExist) { //nolint:gosec // G703: see above
		return orphanGitFile(admin)
	}

	// `git worktree add` always writes commondir, and git finds the common
	// directory through it. An admin directory without one is a whole
	// repository, even at worktrees/<name>: a submodule's, or one moved
	// away with --separate-git-dir. Short of that, a directory in the
	// worktrees directory of a repository is a worktree's that lost it.
	c, err := os.ReadFile(filepath.Join(admin, "commondir")) //nolint:gosec // G703: see above
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

	// git fails in a worktree whose common directory it cannot open, and
	// in every other worktree of that repository too.
	if _, err := os.Stat(common); err != nil || !isGitDir(common) { //nolint:gosec // G703: see above
		why := common + " is not a git repository"
		if errors.Is(err, fs.ErrNotExist) {
			why = common + " is gone"
		}
		if holder := holdingRepo(admin); holder != "" && holder != realPath(common) {
			return gitFile{admin: admin, commonDir: holder, damage: "commondir names " + why}, true
		}
		return gitFile{admin: admin, commonDir: common, repoGone: true, damage: "its git directory " + why}, true
	}
	// The admin directory may now belong to another worktree (git reuses a
	// name freed by a prune), or this one was moved away from where git
	// expects it. Either way the repository no longer tracks this directory.
	intact, movedFrom, damage := backLink(admin, dotGit)
	if damage == "" && isGone(filepath.Join(admin, "HEAD")) {
		damage = "HEAD is missing from " + admin
	}
	return gitFile{admin: admin, commonDir: common, orphaned: !intact, movedFrom: movedFrom, damage: damage}, true
}

// holdingRepo is the repository whose worktrees directory holds admin, or
// "" if there is none. It is resolved: git itself names it that way.
func holdingRepo(admin string) string {
	parent := filepath.Dir(admin)
	if filepath.Base(parent) != "worktrees" || !isGitDir(filepath.Dir(parent)) {
		return ""
	}
	return realPath(filepath.Dir(parent))
}

// backLink checks the admin directory's gitdir file, the link `git
// worktree repair` fixes: whether it names dotGit and, when it names a
// .git file that is gone, the directory git still expects the worktree
// in. A missing or empty link is damage, not proof that the repository
// dropped the worktree: git still works in it, yet `git worktree list`
// leaves it out and `git worktree prune` deletes its admin directory.
// When the link cannot be read for any other reason, it is assumed intact.
func backLink(admin, dotGit string) (intact bool, movedFrom, damage string) {
	content, err := os.ReadFile(filepath.Join(admin, "gitdir")) //nolint:gosec // G703: paths git wrote, see readGitFile
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
	want, err := os.Stat(target) //nolint:gosec // G703: paths git wrote, see readGitFile
	if errors.Is(err, fs.ErrNotExist) {
		return false, filepath.Dir(target), ""
	}
	if err != nil {
		return true, "", ""
	}
	got, err := os.Stat(dotGit)
	// SameFile, not path equality: symlinks, and case on Windows and macOS.
	return err != nil || os.SameFile(want, got), "", ""
}

// orphanGitFile explains a .git file whose admin directory is gone. It
// must not mistake a submodule for a worktree: a submodule checked out at
// worktrees/<name> keeps its git directory at
// <super>/.git/modules/worktrees/<name>, which looks the same.
func orphanGitFile(admin string) (gitFile, bool) {
	common := filepath.Dir(filepath.Dir(admin))
	if filepath.Base(filepath.Dir(admin)) != "worktrees" {
		return gitFile{}, false
	}
	info, err := os.Stat(common) //nolint:gosec // G703: paths git wrote, see readGitFile
	if errors.Is(err, fs.ErrNotExist) {
		// Nothing left to check. A repository inside another git directory
		// is most likely that submodule layout, so it is not trusted.
		if insideGitDir(common) {
			return gitFile{}, false
		}
		return gitFile{admin: admin, commonDir: common, orphaned: true, repoGone: true}, true
	}
	// <super>/.git/modules is not a repository, while a submodule's own
	// repository (<super>/.git/modules/<name>) is and may have worktrees.
	if err != nil || !info.IsDir() || !isGitDir(common) {
		return gitFile{}, false
	}
	return gitFile{admin: admin, commonDir: common, orphaned: true}, true
}

// insideGitDir reports whether path lies below a git directory: one named
// .git, even if it is gone, or any existing one, such as a superproject's
// --separate-git-dir.
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

// isGitDir mirrors git's own test (is_git_directory in setup.c): a
// repository has HEAD, objects and refs.
func isGitDir(dir string) bool {
	for _, name := range []string{"HEAD", "objects", "refs"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil { //nolint:gosec // G703: paths git wrote, see readGitFile
			return false
		}
	}
	return true
}
