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
