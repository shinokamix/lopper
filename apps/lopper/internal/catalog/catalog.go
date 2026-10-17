// Package catalog remembers where scans met repositories, so the next run
// lists them before its walk. It changes how soon a scan finds worktrees,
// never which ones.
package catalog

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"

	"github.com/rogpeppe/go-internal/lockedfile"

	"github.com/shinokamix/lopper/apps/lopper/internal/pathx"
)

const version = 1

type file struct {
	Version int      `json:"version"`
	Places  []string `json:"places"`
}

// Catalog is the file that keeps the places. The zero Catalog keeps
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

// Known returns the places kept. A missing or unreadable file, or one of
// another version, gives none.
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

// Remember replaces the places kept below roots with places, which a
// complete scan of roots met. Places outside roots stay.
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

	kept := slices.DeleteFunc(c.Known(), func(place string) bool { return pathx.Below(roots, place) })
	kept = slices.Compact(slices.Sorted(slices.Values(append(kept, places...))))
	data, err := json.Marshal(file{Version: version, Places: kept})
	if err != nil {
		return err
	}
	return writeFile(c.path, data)
}

func writeFile(path string, data []byte) error {
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
