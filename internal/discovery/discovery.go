// Package discovery finds git repositories on disk and lists their
// linked worktrees. It decides *where* worktrees are, never whether
// they are safe to delete.
package discovery

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/charlievieth/fastwalk"

	"github.com/shinokamix/lopper/internal/gitx"
	"github.com/shinokamix/lopper/internal/lopper"
)

// Options control a discovery run.
type Options struct {
	Roots   []string // walked in full: no directory below them is skipped
	Listers int      // concurrent `git worktree list` processes; at least 1
}

// Scan walks opts.Roots and calls emit for every linked worktree found,
// including orphaned ones that no repository tracks anymore, moved ones
// at the path they were moved to, and unconfirmed ones, whose .git file
// names a repository that did not list them, or could not be listed.
// emit may be called concurrently. A missing or unreadable root is an
// error; unreadable directories below a root are skipped.
func Scan(ctx context.Context, git gitx.Runner, opts Options, emit func(lopper.Worktree)) error {
	gitDirs := make(chan string, 64)
	var listed sync.Map // real paths of worktrees reported by their repository

	var (
		mu         sync.Mutex
		missing    = map[string]lopper.Worktree{} // .git file gone, by recordKey
		orphans    = map[string]orphan{}          // by real path
		candidates = map[string]orphan{}          // tracked, says the .git file; by real path
		listErrs   = map[string]string{}          // why `git worktree list` failed, by real git directory
		repos      = map[string]lopper.Repo{}     // repositories git could list, by real git directory
	)

	var wg sync.WaitGroup
	for range max(opts.Listers, 1) {
		wg.Go(func() {
			for gitDir := range gitDirs {
				repo, err := listRepo(ctx, git, gitDir, func(wt lopper.Worktree) {
					if !present(&wt) { // unless it was moved, see below
						mu.Lock()
						defer mu.Unlock()
						missing[recordKey(gitDir, wt.Path)] = wt
						return
					}
					listed.Store(realPath(wt.Path), struct{}{})
					emit(wt)
				})
				mu.Lock()
				if err != nil {
					listErrs[realPath(gitDir)] = firstLine(err.Error())
				} else if repo.Path != "" {
					repos[realPath(gitDir)] = repo
				}
				mu.Unlock()
			}
		})
	}

	err := findRepos(ctx, opts,
		func(gitDir string) { gitDirs <- gitDir },
		func(dir string, gf gitFile) {
			mu.Lock()
			defer mu.Unlock()
			if gf.orphaned {
				wt := orphanWorktree(dir, gf.commonDir)
				orphans[realPath(wt.Path)] = orphan{wt, gf}
				return
			}
			candidates[realPath(dir)] = orphan{lopper.Worktree{Path: filepath.Clean(dir)}, gf}
		})
	close(gitDirs)
	wg.Wait()

	// A worktree whose .git file points to a moved repository still shows up
	// in that repository's list when the walk reached it; only the rest are
	// orphans. A worktree moved to another directory shows up in the list as
	// a missing one, and is reported once, where it is now. Hence both wait
	// for every repository to be listed.
	if ctx.Err() == nil {
		// Sorted, so that when copies of one worktree claim the same
		// missing entry, the same one wins every time.
		for _, key := range slices.Sorted(maps.Keys(orphans)) {
			o := orphans[key]
			if _, ok := listed.Load(key); ok {
				continue
			}
			if o.gf.movedFrom != "" {
				// Another repository may have used and left the same path.
				from := recordKey(o.gf.commonDir, o.gf.movedFrom)
				if stale, ok := missing[from]; ok {
					delete(missing, from)
					emit(movedWorktree(stale, o.wt.Path))
					continue
				}
			}
			emit(o.wt)
		}
		for _, key := range slices.Sorted(maps.Keys(missing)) {
			emit(missing[key])
		}
		// Found through its .git file, which the repository did not
		// confirm: reported rather than lost when git fails.
		for _, key := range slices.Sorted(maps.Keys(candidates)) {
			if _, ok := listed.Load(key); ok {
				continue
			}
			c := candidates[key]
			repo, ok := repos[realPath(c.gf.commonDir)]
			if !ok {
				repo = lopper.Repo{Path: repoPath(c.gf.commonDir)}
			}
			why := "its repository does not list it"
			if err, ok := listErrs[realPath(c.gf.commonDir)]; ok {
				why = "could not list its worktrees: " + err
			}
			if c.gf.damage != "" {
				why = c.gf.damage
			}
			emit(unconfirmedWorktree(c.wt.Path, repo, c.gf.admin, why))
		}
	}
	return err
}

// ErrNotListed is returned by Lookup for a directory that its repository
// does not list as a linked worktree.
var ErrNotListed = errors.New("not a worktree its repository lists")

// Lookup finds the linked worktree at path as its repository lists it
// now, marked as Scan marks it. repo is the repository's path, as in
// lopper.Repo; it may be empty when path still exists, and is then found
// through path's .git file.
func Lookup(ctx context.Context, git gitx.Runner, repo, path string) (lopper.Worktree, error) {
	gitDir := repo
	switch {
	case repo == "":
		gf, ok := readGitFile(filepath.Join(path, ".git"))
		if !ok || gf.orphaned || gf.repoGone {
			return lopper.Worktree{}, ErrNotListed
		}
		gitDir = gf.commonDir
	case !isGitDir(repo):
		gitDir = filepath.Join(repo, ".git")
	}
	want := realPath(path)
	var (
		wt    lopper.Worktree
		found bool
	)
	_, err := listRepo(ctx, git, gitDir, func(w lopper.Worktree) {
		if realPath(w.Path) == want {
			wt, found = w, true
		}
	})
	if err != nil {
		return lopper.Worktree{}, err
	}
	if !found {
		return lopper.Worktree{}, ErrNotListed
	}
	present(&wt)
	return wt, nil
}

// present reports whether a worktree its repository lists is still on
// disk, and marks it unconfirmed when git lists it but can no longer use
// its admin directory. git does not call a locked worktree prunable, so
// its .git file is checked, as backLink does: the directory may have been
// recreated.
func present(wt *lopper.Worktree) bool {
	dotGit := filepath.Join(wt.Path, ".git")
	if wt.Prunable || isGone(dotGit) {
		return false
	}
	if gf, ok := readGitFile(dotGit); ok {
		wt.Unconfirmed = gf.damage
	}
	return true
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return line
}

// orphan is a linked worktree found through its .git file, and what that
// file tells about its repository.
type orphan struct {
	wt lopper.Worktree
	gf gitFile
}

// recordKey identifies a repository's record of a worktree at path.
func recordKey(gitDir, path string) string {
	return realPath(gitDir) + "\x00" + realPath(path)
}

// isGone reports whether path definitely does not exist.
func isGone(path string) bool {
	_, err := os.Lstat(path) //nolint:gosec // G703: paths git wrote, see readGitFile
	return errors.Is(err, fs.ErrNotExist)
}

// findRepos reports the common git directory of every repository met
// during the walk, once. A .git directory belongs to a main worktree; a
// .git file leads to a git directory elsewhere, which may live outside
// the roots: that of a linked worktree's main repository, a submodule,
// or a --separate-git-dir or ".bare" layout. Bare repositories have no
// .git at all and are recognized by their worktrees directory. The
// repositories of submodules live inside another git directory, where the
// walk does not go, and are looked up there: their checkout may be gone
// while their worktrees are not. Every linked worktree met also goes to
// linked, with what its .git file tells about the repository.
//
// Symbolic links to directories are followed. Every physical directory
// is walked once, whichever path reaches it first, so neither a link
// back to an ancestor nor overlapping roots make the walk repeat itself.
func findRepos(ctx context.Context, opts Options, found func(gitDir string), linked func(dir string, gf gitFile)) error {
	var seen sync.Map
	var report func(gitDir string)
	report = func(gitDir string) {
		// The same repo may be reached via a symlinked path.
		if _, dup := seen.LoadOrStore(realPath(gitDir), struct{}{}); !dup {
			found(gitDir)
			submodules(gitDir, report)
		}
	}
	visited := fastwalk.NewEntryFilter() // by device and inode, across roots
	conf := fastwalk.DefaultConfig
	conf.ToSlash = false // keep native separators under MSYS/Git Bash: paths are reported as found

	for _, root := range opts.Roots {
		root = filepath.Clean(root)
		if err := checkRoot(root); err != nil {
			return err
		}
		err := fastwalk.Walk(&conf, root, func(path string, d fs.DirEntry, err error) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			if err != nil {
				if path == root {
					return fmt.Errorf("scan %s: %w", root, err)
				}
				return nil // unreadable entries below a root are skipped, not fatal
			}
			typ, link := d.Type(), d.Type()&fs.ModeSymlink != 0
			if link {
				info, err := fastwalk.StatDirEntry(path, d)
				if err != nil {
					return nil //nolint:nilerr // dangling, or a loop of links: nothing to walk
				}
				typ = info.Mode().Type()
			}
			if typ.IsDir() && visited.Entry(path, d) {
				return fs.SkipDir
			}
			name := d.Name()
			if name == ".git" {
				if typ.IsDir() {
					report(path)
					return fs.SkipDir
				}
				if typ.IsRegular() {
					gf, ok := readGitFile(path)
					if ok && gf.admin != gf.commonDir {
						linked(physical(opts.Roots, filepath.Dir(path)), gf)
					}
					if ok && !gf.repoGone {
						// Even without this worktree, the repository may have others.
						report(gf.commonDir)
					}
				}
				return nil
			}
			if name == "worktrees" && typ.IsDir() && isGitDir(filepath.Dir(path)) {
				report(filepath.Dir(path)) // a bare repository
				return fs.SkipDir
			}
			if link && typ.IsDir() {
				return fastwalk.ErrTraverseLink
			}
			return nil
		})
		if err != nil {
			return err
		}
	}
	return nil
}

// submodules calls found for every submodule repository kept in gitDir:
// in modules/<submodule path>, in modules/ of those for nested ones (via
// found), and in worktrees/<id>/modules for those of linked worktrees.
func submodules(gitDir string, found func(gitDir string)) {
	modules(filepath.Join(gitDir, "modules"), found)
	ids, _ := os.ReadDir(filepath.Join(gitDir, "worktrees"))
	for _, id := range ids {
		if id.IsDir() {
			modules(filepath.Join(gitDir, "worktrees", id.Name(), "modules"), found)
		}
	}
}

// modules finds the repositories below dir. A submodule path has several
// components when the submodule is not at the top of its superproject.
// Symbolic links are not followed: git creates none there.
func modules(dir string, found func(gitDir string)) {
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		path := filepath.Join(dir, e.Name())
		if isGitDir(path) {
			found(path)
		} else {
			modules(path, found)
		}
	}
}

// physical is where dir really is, so that a directory the walk may reach
// by several paths is reported the same way every time: its resolved
// path, but below the first root that contains it, as that root is given.
func physical(roots []string, dir string) string {
	resolved := realPath(dir)
	for _, root := range roots {
		rel, err := filepath.Rel(realPath(root), resolved)
		if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return filepath.Join(filepath.Clean(root), rel)
		}
	}
	return resolved
}

// realPath resolves symlinks so that one directory reached by two paths
// is recognised. Of a path that is gone, what is left is resolved: git
// may record a missing worktree through a symlink the walk does not use.
func realPath(path string) string {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return resolved
	}
	parent := filepath.Dir(path)
	if parent == path {
		return path
	}
	return filepath.Join(realPath(parent), filepath.Base(path))
}

func checkRoot(root string) error {
	info, err := os.Stat(root)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return fmt.Errorf("%s: no such directory", root)
	case err != nil:
		return fmt.Errorf("scan %s: %w", root, err)
	case !info.IsDir():
		return fmt.Errorf("%s: not a directory", root)
	}
	return nil
}

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

// orphanWorktree describes a worktree from its directory alone: with the
// admin directory gone, its branch and HEAD are unknown.
func orphanWorktree(dir, commonDir string) lopper.Worktree {
	path := filepath.Clean(dir)
	return lopper.Worktree{
		ID:       lopper.ID(path),
		Path:     path,
		Repo:     lopper.Repo{Path: repoPath(commonDir)},
		Orphaned: true,
		Origin:   classifyOrigin(path),
	}
}

// unconfirmedWorktree describes a worktree from its directory and its
// admin directory, which tells the branch or commit it has checked out and
// whether it is locked. A lock that cannot be ruled out is assumed: it is
// the user's explicit wish to keep the worktree.
func unconfirmedWorktree(dir string, repo lopper.Repo, admin, why string) lopper.Worktree {
	path := filepath.Clean(dir)
	wt := lopper.Worktree{
		ID:          lopper.ID(path),
		Path:        path,
		Repo:        repo,
		Locked:      !isGone(filepath.Join(admin, "locked")),
		Unconfirmed: why,
		Origin:      classifyOrigin(path),
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
	wt.Prunable = false
	wt.MovedFrom = stale.Path
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
		emit(lopper.Worktree{
			ID:       lopper.ID(path),
			Path:     path,
			Repo:     repo,
			Branch:   e.Branch,
			Head:     e.Head,
			Locked:   e.Locked,
			Prunable: e.Prunable,
			Origin:   classifyOrigin(path),
		})
	}
	return repo, nil
}
