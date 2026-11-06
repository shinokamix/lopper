package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
)

func defaultRoots() ([]string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("locate home directory: %w", err)
	}
	return withTemps([]string{home}, tempDirs()), nil
}

func tempDirs() []string {
	if runtime.GOOS == "windows" {
		return []string{os.Getenv("TMP"), os.Getenv("TEMP"), os.TempDir()}
	}
	// macOS gives each user its own $TMPDIR. /tmp and /var/tmp are shared,
	// and stay in use when a sandbox points $TMPDIR elsewhere.
	return []string{os.Getenv("TMPDIR"), "/tmp", "/var/tmp"}
}

// withTemps resolves the temporary directories, since on macOS /tmp is
// /private/tmp.
func withTemps(roots, temps []string) []string {
	var added []string
	for _, dir := range temps {
		if dir == "" {
			continue
		}
		resolved, err := filepath.EvalSymlinks(dir)
		if err != nil {
			continue
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

func walkReaches(root, dir string) bool {
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		resolved = root
	}
	rel, err := filepath.Rel(resolved, dir)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func under(roots []os.FileInfo, path string) bool {
	dir, err := filepath.EvalSymlinks(path)
	if err != nil {
		return false
	}
	for {
		if info, err := os.Stat(dir); err == nil && slices.ContainsFunc(roots, func(root os.FileInfo) bool { return os.SameFile(root, info) }) {
			return true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return false
		}
		dir = parent
	}
}

func physicalRoots(args []string) ([]string, error) {
	if len(args) == 0 {
		roots, err := defaultRoots()
		if err != nil {
			return nil, err
		}
		args = roots
	}
	out := make([]string, 0, len(args))
	for _, a := range args {
		root, err := physical(a)
		if err != nil {
			return nil, err
		}
		out = append(out, root)
	}
	return out, nil
}

// physical resolves path the way the OS does. filepath.Abs and filepath.Join
// drop "dir/.." before any symlink is followed, so "link/.." would name the
// folder holding link instead of the parent of its target.
func physical(path string) (string, error) {
	if path != "" && !filepath.IsAbs(path) && filepath.VolumeName(path) == "" && !os.IsPathSeparator(path[0]) {
		cwd, err := os.Getwd()
		if err != nil {
			return "", err
		}
		if cwd, err = filepath.EvalSymlinks(cwd); err != nil {
			return "", err
		}
		path = cwd + string(filepath.Separator) + path
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	return filepath.Abs(resolved)
}
