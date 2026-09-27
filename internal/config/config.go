// Package config holds user settings. TODO: load TOML from os.UserConfigDir().
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
)

type Config struct {
	// Roots are the home directory and the temporary directories that a
	// walk of it would not reach.
	Roots []string
	// SkipNames are directory base names skipped at any depth: they never
	// hold repositories a user would work in.
	SkipNames map[string]bool
	// SkipPaths are absolute directories skipped: caches and toolchains
	// under the home directory whose names are too common to skip anywhere
	// (~/code/Library is a fine project name).
	SkipPaths map[string]bool
}

func Default() (Config, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Config{}, fmt.Errorf("locate home directory: %w", err)
	}
	return defaults(home, tempDirs()), nil
}

func defaults(home string, temps []string) Config {
	c := Config{SkipNames: set(skipNames), SkipPaths: homePaths(home)}
	c.Roots = c.withTemps([]string{home}, temps)
	return c
}

// tempDirs are where programs put throwaway directories, among them the
// checkouts coding agents make for a task and the fixtures of their
// tests. On Unix they lie outside the home directory; on Windows inside
// it, but under AppData, which is skipped.
func tempDirs() []string {
	if runtime.GOOS == "windows" {
		// os.TempDir is the first set of TMP, TEMP and USERPROFILE.
		return []string{os.Getenv("TMP"), os.Getenv("TEMP"), os.TempDir()}
	}
	// $TMPDIR is per user on macOS; /tmp and /var/tmp are shared, and
	// stay in use when a sandbox points $TMPDIR elsewhere.
	return []string{os.Getenv("TMPDIR"), "/tmp", "/var/tmp"}
}

// withTemps adds to roots each temporary directory that exists and that a
// walk of roots or of another temporary directory would not reach. Paths
// are resolved first: on macOS /tmp is /private/tmp. roots are kept as
// they are, so that SkipPaths, derived from them, still match.
func (c Config) withTemps(roots, temps []string) []string {
	var added []string
	for _, dir := range temps {
		if dir == "" {
			continue
		}
		resolved, err := filepath.EvalSymlinks(dir)
		if err != nil {
			continue // missing, or not ours to read: nothing to find there
		}
		if info, err := os.Stat(resolved); err != nil || !info.IsDir() {
			continue
		}
		reached := func(root string) bool { return c.walkReaches(root, resolved) }
		if slices.ContainsFunc(roots, reached) || slices.ContainsFunc(added, reached) {
			continue
		}
		added = slices.DeleteFunc(added, func(t string) bool { return c.walkReaches(resolved, t) })
		added = append(added, resolved)
	}
	return append(roots, added...)
}

// walkReaches reports whether a walk of root descends into dir, which is
// a resolved path: dir lies below root and not below a skipped directory.
func (c Config) walkReaches(root, dir string) bool {
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		resolved = root
	}
	rel, err := filepath.Rel(resolved, dir)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false
	}
	if rel == "." {
		return true
	}
	path := root
	for name := range strings.SplitSeq(rel, string(filepath.Separator)) {
		path = filepath.Join(path, name)
		if c.SkipNames[name] || c.SkipPaths[path] {
			return false
		}
	}
	return true
}

var skipNames = []string{
	"node_modules", ".venv", "__pycache__", ".pnpm-store", ".yarn",
	".tox", ".mypy_cache", ".pytest_cache",
}

var skipHomePaths = []string{
	"Library", "AppData", ".Trash", ".cache", ".npm", ".cargo", ".rustup",
	".gradle", ".m2", filepath.Join("go", "pkg"),
}

func homePaths(home string) map[string]bool {
	paths := make([]string, len(skipHomePaths))
	for i, p := range skipHomePaths {
		paths[i] = filepath.Join(home, p)
	}
	return set(paths)
}

func set(items []string) map[string]bool {
	m := make(map[string]bool, len(items))
	for _, it := range items {
		m[it] = true
	}
	return m
}
