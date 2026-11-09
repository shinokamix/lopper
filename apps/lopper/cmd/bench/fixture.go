package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

const (
	walkDirs    = 1000
	walkSubdirs = 30
	walkFiles   = 3
	repos       = 12
	depDirs     = 40
	depFiles    = 25
	bigDepDirs  = 500
	bigDepFiles = 20
)

var linked = []string{"merged", "dirty", "unpushed", "squashed"}

func cases(root string) []scanCase {
	return []scanCase{
		{name: "walk", dir: filepath.Join(root, "walk")},
		{name: "worktrees", dir: filepath.Join(root, "worktrees"), worktrees: repos * len(linked)},
		{name: "big worktrees", dir: filepath.Join(root, "big"), worktrees: len(linked)},
	}
}

func gitEnv(root string) []string {
	return []string{
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL=" + filepath.Join(root, ".gitconfig"),
		"GIT_AUTHOR_DATE=2026-01-01T00:00:00Z",
		"GIT_COMMITTER_DATE=2026-01-01T00:00:00Z",
	}
}

type fixture struct {
	root string
	err  error
}

func makeFixture(root string) error {
	f := &fixture{root: root}
	f.write(filepath.Join(root, ".gitconfig"), "[user]\n\tname = lopper\n\temail = bench@lopper.invalid\n[init]\n\tdefaultBranch = main\n")
	if f.err != nil {
		return f.err
	}
	var wg sync.WaitGroup
	errs := make([]error, walkDirs+repos+1)
	inParallel := func(i int, build func(f *fixture)) {
		wg.Go(func() {
			f := &fixture{root: root}
			build(f)
			errs[i] = f.err
		})
	}
	for i := range walkDirs {
		inParallel(i, func(f *fixture) {
			for j := range walkSubdirs {
				f.files(filepath.Join(root, "walk", fmt.Sprintf("d%03d", i), fmt.Sprintf("s%02d", j)), walkFiles)
			}
		})
	}
	for i := range repos {
		inParallel(walkDirs+i, func(f *fixture) {
			f.repo(filepath.Join(root, "worktrees", fmt.Sprintf("repo%02d", i)), depDirs, depFiles)
		})
	}
	inParallel(walkDirs+repos, func(f *fixture) {
		f.repo(filepath.Join(root, "big", "repo"), bigDepDirs, bigDepFiles)
	})
	wg.Wait()
	return errors.Join(errs...)
}

func (f *fixture) repo(dir string, deps, files int) {
	lines := make([]string, 100)
	for i := range lines {
		lines[i] = fmt.Sprintf("line %d", i)
	}
	text := func(edits map[int]string) string {
		out := append([]string(nil), lines...)
		for i, l := range edits {
			out[i] = l
		}
		return strings.Join(out, "\n") + "\n"
	}
	path := func(kind string) string { return dir + "-" + kind }

	f.git(f.root, "init", "-q", dir)
	f.write(filepath.Join(dir, ".gitignore"), "node_modules/\n")
	f.write(filepath.Join(dir, "notes.txt"), text(nil))
	f.files(filepath.Join(dir, "src"), 20)
	f.git(dir, "add", "-A")
	f.git(dir, "commit", "-q", "-m", "init")
	for _, kind := range linked {
		f.git(dir, "worktree", "add", "-q", "-b", kind, path(kind))
	}
	f.write(filepath.Join(path("dirty"), "draft.txt"), "draft\n")
	f.write(filepath.Join(path("unpushed"), "notes.txt"), text(map[int]string{50: "work"}))
	f.git(path("unpushed"), "commit", "-q", "-am", "work")
	f.write(filepath.Join(path("squashed"), "notes.txt"), text(map[int]string{10: "fix"}))
	f.git(path("squashed"), "commit", "-q", "-am", "fix")
	f.write(filepath.Join(dir, "notes.txt"), text(map[int]string{10: "fix", 90: "more"}))
	f.git(dir, "commit", "-q", "-am", "fix and more")

	checkouts := []string{dir}
	for _, kind := range linked {
		checkouts = append(checkouts, path(kind))
	}
	for _, checkout := range checkouts {
		for k := range deps {
			f.files(filepath.Join(checkout, "node_modules", fmt.Sprintf("pkg%03d", k)), files)
		}
	}
}

func (f *fixture) git(dir string, args ...string) {
	if f.err != nil {
		return
	}
	full := append([]string{"-C", dir}, args...)
	cmd := exec.Command("git", full...)
	cmd.Env = append(os.Environ(), gitEnv(f.root)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		f.err = fmt.Errorf("git %s: %w\n%s", strings.Join(args, " "), err, out)
	}
}

func (f *fixture) files(dir string, n int) {
	for i := range n {
		f.write(filepath.Join(dir, fmt.Sprintf("f%02d.txt", i)), strings.Repeat("x", 512))
	}
}

func (f *fixture) write(path, content string) {
	if f.err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		f.err = err
		return
	}
	f.err = os.WriteFile(path, []byte(content), 0o600)
}
