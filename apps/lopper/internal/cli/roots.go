package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
)

// defaultRoots are what lopper scans when no path is given: the home
// directory and the temporary directories a walk of it would miss. The walk
// skips nothing below them, since caches, Library, AppData and node_modules
// can hold worktrees too.
func defaultRoots() ([]string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("locate home directory: %w", err)
	}
	return withTemps([]string{home}, tempDirs()), nil
}

// tempDirs are where programs put throwaway directories, such as the
// checkouts coding agents make for a task and their test fixtures. On Unix
// they lie outside the home directory, and on Windows inside it.
func tempDirs() []string {
	if runtime.GOOS == "windows" {
		// os.TempDir is the first set of TMP, TEMP and USERPROFILE.
		return []string{os.Getenv("TMP"), os.Getenv("TEMP"), os.TempDir()}
	}
	// $TMPDIR is per user on macOS; /tmp and /var/tmp are shared, and
	// stay in use when a sandbox points $TMPDIR elsewhere.
	return []string{os.Getenv("TMPDIR"), "/tmp", "/var/tmp"}
}

// withTemps adds to roots each existing temporary directory that a walk of
// roots or of another temporary directory would miss. It resolves the
// temporary directories first, since on macOS /tmp is /private/tmp, and
// keeps roots as given.
func withTemps(roots, temps []string) []string {
	var added []string
	for _, dir := range temps {
		if dir == "" {
			continue
		}
		resolved, err := filepath.EvalSymlinks(dir)
		if err != nil {
			continue // missing or unreadable, so nothing to find
		}
		if !readableDir(resolved) {
			continue
		}
		reached := func(root string) bool { return walkReaches(root, resolved) }
		if slices.ContainsFunc(roots, reached) || slices.ContainsFunc(added, reached) {
			continue
		}
		added = slices.DeleteFunc(added, func(t string) bool { return walkReaches(resolved, t) })
		added = append(added, resolved)
	}
	return append(roots, added...)
}

// readableDir reports whether the walk can list dir. An unlistable root is a
// fatal error, which only a path the user chose deserves.
func readableDir(dir string) bool {
	// Stat first, because opening a FIFO blocks until something writes to it.
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		return false
	}
	f, err := os.Open(dir)
	if err != nil {
		return false
	}
	return f.Close() == nil
}

// walkReaches reports whether dir, a resolved path, lies below root, so a
// walk of root reaches it.
func walkReaches(root, dir string) bool {
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		resolved = root
	}
	rel, err := filepath.Rel(resolved, dir)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
