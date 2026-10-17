// Package pathx compares paths by name, without resolving symlinks.
package pathx

import (
	"path/filepath"
	"slices"
	"strings"
)

// Within returns path relative to dir, and false if path lies outside dir.
func Within(dir, path string) (string, bool) {
	rel, err := filepath.Rel(dir, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	return rel, true
}

// Below reports whether path lies within any of roots.
func Below(roots []string, path string) bool {
	return slices.ContainsFunc(roots, func(root string) bool {
		_, ok := Within(filepath.Clean(root), path)
		return ok
	})
}
