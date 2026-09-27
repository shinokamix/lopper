// Package config holds user settings. TODO: load TOML from os.UserConfigDir().
package config

import (
	"fmt"
	"os"
	"path/filepath"
)

type Config struct {
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
	return Config{
		Roots:     []string{home},
		SkipNames: set(skipNames),
		SkipPaths: homePaths(home),
	}, nil
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
