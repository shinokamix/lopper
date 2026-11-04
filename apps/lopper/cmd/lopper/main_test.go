package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/rogpeppe/go-internal/testscript"
)

func TestMain(m *testing.M) {
	testscript.Main(m, map[string]func(){
		"lopper": func() { os.Exit(run()) },
	})
}

// TestUpdateBuilds compiles the real entry point in a separate module so
// Go records its clean Git tag without changing this checkout.
func TestUpdateBuilds(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	cacheDirs, err := exec.Command("go", "env", "GOCACHE", "GOMODCACHE").Output()
	if err != nil {
		t.Fatal(err)
	}
	cache := strings.Split(strings.TrimSpace(string(cacheDirs)), "\n")
	testscript.Run(t, testscript.Params{
		Dir: "testdata/build",
		Setup: func(env *testscript.Env) error {
			env.Setenv("GOCACHE", strings.TrimSpace(cache[0]))
			env.Setenv("GOMODCACHE", strings.TrimSpace(cache[1]))
			for name, source := range map[string]string{
				"main.go": filepath.Join("cmd", "lopper", "main.go"),
				"go.mod":  "go.mod",
				"go.sum":  "go.sum",
			} {
				data, err := os.ReadFile(filepath.Join(root, source))
				if err != nil {
					return err
				}
				if name == "go.mod" {
					data = []byte(strings.Replace(string(data), "module github.com/shinokamix/lopper/apps/lopper", "module github.com/shinokamix/lopper/apps/lopper/buildtest", 1) +
						fmt.Sprintf("\nrequire github.com/shinokamix/lopper/apps/lopper v0.0.0\nreplace github.com/shinokamix/lopper/apps/lopper => %q\n", root))
				}
				if err := os.WriteFile(filepath.Join(env.WorkDir, name), data, 0o600); err != nil {
					return err
				}
			}
			return nil
		},
	})
}

func TestScript(t *testing.T) {
	testscript.Run(t, testscript.Params{
		Dir: "testdata/script",
		Setup: func(env *testscript.Env) error {
			gitconfig := filepath.Join(env.WorkDir, ".gitconfig")
			content := "[user]\n\tname = lopper\n\temail = test@lopper.invalid\n[init]\n\tdefaultBranch = main\n"
			if err := os.WriteFile(gitconfig, []byte(content), 0o600); err != nil {
				return err
			}
			env.Setenv("GIT_CONFIG_NOSYSTEM", "1")
			env.Setenv("GIT_CONFIG_GLOBAL", gitconfig)
			return nil
		},
		Cmds: map[string]func(ts *testscript.TestScript, neg bool, args []string){
			"exits": exits,
		},
	})
}

func exits(ts *testscript.TestScript, neg bool, args []string) {
	if neg || len(args) < 2 {
		ts.Fatalf("usage: exits status program [args...]")
	}
	want, err := strconv.Atoi(args[0])
	if err != nil {
		ts.Fatalf("exits: bad status %q", args[0])
	}
	got := 0
	if err := ts.Exec(args[1], args[2:]...); err != nil {
		exit, ok := errors.AsType[*exec.ExitError](err)
		if !ok {
			ts.Fatalf("exits: %v", err)
		}
		got = exit.ExitCode()
	}
	if got != want {
		ts.Fatalf("%s exited with %d, want %d", args[1], got, want)
	}
}
