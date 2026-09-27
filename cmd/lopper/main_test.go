package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/rogpeppe/go-internal/testscript"
)

func TestMain(m *testing.M) {
	testscript.Main(m, map[string]func(){
		"lopper": func() { os.Exit(run()) },
	})
}

// TestScript runs the end-to-end scenarios in testdata/script against
// real git repositories created inside each script's $WORK directory.
func TestScript(t *testing.T) {
	testscript.Run(t, testscript.Params{
		Dir: "testdata/script",
		Setup: func(env *testscript.Env) error {
			// Isolate git from the developer's machine: no system or global
			// config (and therefore no global hooks), fixed identity.
			gitconfig := filepath.Join(env.WorkDir, ".gitconfig")
			content := "[user]\n\tname = lopper\n\temail = test@lopper.invalid\n[init]\n\tdefaultBranch = main\n"
			if err := os.WriteFile(gitconfig, []byte(content), 0o600); err != nil {
				return err
			}
			env.Setenv("GIT_CONFIG_NOSYSTEM", "1")
			env.Setenv("GIT_CONFIG_GLOBAL", gitconfig)
			return nil
		},
	})
}
