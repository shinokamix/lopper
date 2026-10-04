// Package catalog remembers, between runs, where scans met repositories,
// so that the next scan lists them before its walk. It changes only how
// soon worktrees are found: a scan finds the same ones without it.
package catalog

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/rogpeppe/go-internal/lockedfile"
)

// version changes whenever what a place means does: a file of another
// version is ignored.
const version = 1

type file struct {
	Version int      `json:"version"`
	Places  []string `json:"places"`
}

// Catalog is the file the places are kept in. The zero Catalog keeps
// nothing.
type Catalog struct{ path string }

// Open returns the catalog in the user's cache directory, or the zero
// Catalog when there is none.
func Open() Catalog {
	dir, err := os.UserCacheDir()
	if err != nil {
		return Catalog{}
	}
	return Catalog{filepath.Join(dir, "lopper", "repos.json")}
}

// Known returns the places kept, or none when there are none to read:
// the file is missing, unreadable, or of another version.
func (c Catalog) Known() []string {
	if c.path == "" {
		return nil
	}
	data, err := os.ReadFile(c.path)
	if err != nil {
		return nil
	}
	var f file
	if json.Unmarshal(data, &f) != nil || f.Version != version {
		return nil
	}
	return f.Places
}

// Remember keeps places, met by a complete scan of roots, instead of the
// places kept below roots: that scan would have met them again had they
// still led to a repository. Places elsewhere stay, for scans of other
// roots.
func (c Catalog) Remember(roots, places []string) error {
	if c.path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(c.path), 0o700); err != nil {
		return err
	}
	unlock, err := lockedfile.MutexAt(c.path + ".lock").Lock()
	if err != nil {
		return err
	}
	defer unlock()

	kept := slices.DeleteFunc(c.Known(), func(place string) bool { return below(roots, place) })
	kept = slices.Compact(slices.Sorted(slices.Values(append(kept, places...))))
	data, err := json.Marshal(file{Version: version, Places: kept})
	if err != nil {
		return err
	}
	return writeFile(c.path, data)
}

// writeFile replaces path with data at once, so that a run reading it
// meanwhile, or two runs writing it, never leave half a file.
func writeFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".repos-*.json")
	if err != nil {
		return err
	}
	_, err = tmp.Write(data)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp.Name(), path)
	}
	if err != nil {
		return errors.Join(err, os.Remove(tmp.Name()))
	}
	return nil
}

// below reports whether a walk of roots reaches path by its name, as the
// roots are given: as discovery takes Known.
func below(roots []string, path string) bool {
	return slices.ContainsFunc(roots, func(root string) bool {
		rel, err := filepath.Rel(filepath.Clean(root), path)
		return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
	})
}
