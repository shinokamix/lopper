// Package archtest enforces the layering described in CONTRIBUTING.md.
package archtest

import (
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const module = "github.com/shinokamix/lopper/apps/lopper"

var layers = map[string][]string{
	"cmd/lopper": {"internal/cli"},

	"internal/lopper":   {},
	"internal/gitx":     {},
	"internal/archtest": {},
	"internal/pathx":    {},
	"internal/update":   {},
	"internal/catalog":  {"internal/pathx"},

	"internal/verdict":   {"internal/lopper"},
	"internal/inspect":   {"internal/lopper", "internal/gitx"},
	"internal/discovery": {"internal/lopper", "internal/gitx", "internal/pathx"},
	"internal/engine":    {"internal/lopper", "internal/gitx", "internal/pathx", "internal/discovery", "internal/inspect", "internal/verdict"},

	"internal/cli": {"internal/lopper", "internal/engine", "internal/catalog", "internal/tui", "internal/update"},
	"internal/tui": {"internal/lopper", "internal/engine"},
}

var restricted = map[string][]string{
	"os/exec":                   {"internal/gitx"},
	"net/http":                  {"internal/update"},
	"charm.land/":               {"internal/tui/..."},
	"github.com/charmbracelet/": {"cmd/lopper", "internal/tui/..."},
	"github.com/spf13/cobra":    {"cmd/lopper", "internal/cli"},
}

type pkg struct {
	ImportPath string
	Imports    []string
}

func TestLayers(t *testing.T) {
	trackSources(t)
	pkgs := listPackages(t)
	seen := map[string]bool{}

	for _, p := range pkgs {
		name, _ := relative(p.ImportPath)
		seen[name] = true

		allowed, ok := layers[name]
		if !ok {
			t.Errorf("%s is not listed in layers: decide which layer it belongs to", name)
			continue
		}
		for _, imp := range p.Imports {
			if dep, internal := relative(imp); internal && !slices.Contains(allowed, dep) {
				t.Errorf("%s must not import %s", name, dep)
			}
			for prefix, users := range restricted {
				if strings.HasPrefix(imp, prefix) && !matchesAny(name, users) {
					t.Errorf("%s must not import %s (allowed only in %v)", name, imp, users)
				}
			}
		}
	}

	for name := range layers {
		if !seen[name] {
			t.Errorf("layers lists %s, but no such package exists", name)
		}
	}
}

func relative(path string) (string, bool) {
	return strings.CutPrefix(path, module+"/")
}

func matchesAny(name string, patterns []string) bool {
	for _, p := range patterns {
		if base, tree := strings.CutSuffix(p, "/..."); tree {
			if name == base || strings.HasPrefix(name, base+"/") {
				return true
			}
		} else if name == p {
			return true
		}
	}
	return false
}

func listPackages(t *testing.T) []pkg {
	t.Helper()
	out, err := exec.Command("go", "list", "-json=ImportPath,Imports", module+"/...").Output()
	if err != nil {
		t.Fatalf("go list: %v", err)
	}
	var pkgs []pkg
	dec := json.NewDecoder(strings.NewReader(string(out)))
	for {
		var p pkg
		if err := dec.Decode(&p); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			t.Fatalf("decode go list output: %v", err)
		}
		pkgs = append(pkgs, p)
	}
	return pkgs
}

// trackSources opens every source because the go test cache only notices
// files the test process touches, not those the `go list` child reads.
func trackSources(t *testing.T) {
	t.Helper()
	root := filepath.Join("..", "..")
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && (d.Name() == ".git" || d.Name() == "testdata") {
			return fs.SkipDir
		}
		if !d.IsDir() && filepath.Ext(path) != ".go" {
			return nil
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		return f.Close()
	})
	if err != nil {
		t.Fatalf("walk sources: %v", err)
	}
}
