// Command bench times `lopper scan` built from a base commit and from this
// checkout, on the same generated repositories, and counts the git processes
// each scan starts. It prints a Markdown report and exits with 1 when a scan
// starts more git processes, or when its median time grows by more than
// -max-slowdown percent.
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

var errRegressed = errors.New("this change makes a scan slower or start more git processes")

func main() {
	rounds := flag.Int("rounds", 10, "timed scans of each build")
	maxSlowdown := flag.Float64("max-slowdown", 20, "slowdown in percent that fails the run, or 0 to only report time")
	runner := flag.String("name", "", "the runner, for the report title")
	flag.Usage = func() {
		fmt.Fprintln(flag.CommandLine.Output(), "usage: bench [flags] [base]\n\nbase defaults to where HEAD leaves origin/main.")
		flag.PrintDefaults()
	}
	flag.Parse()
	if err := run(flag.Arg(0), report{runner: *runner, rounds: *rounds, maxSlowdown: *maxSlowdown}); err != nil {
		fmt.Fprintln(os.Stderr, "bench:", err)
		os.Exit(1)
	}
}

func run(base string, rep report) error {
	if rep.rounds < 1 {
		return errors.New("-rounds must be at least 1")
	}
	if base == "" {
		var err error
		if base, err = git("merge-base", "HEAD", "origin/main"); err != nil {
			return err
		}
	}
	base, err := git("rev-parse", "--verify", "--end-of-options", base+"^{commit}")
	if err != nil {
		return err
	}
	changed, err := lopperChanged(base)
	if err != nil {
		return err
	}
	if !changed {
		fmt.Fprintf(os.Stderr, "lopper is the same as in %s: nothing to compare\n", base)
		return nil
	}
	if rep.base, err = git("rev-parse", "--short", base); err != nil {
		return err
	}

	work, err := os.MkdirTemp("", "lopper-bench-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(work)

	fmt.Fprintf(os.Stderr, "building lopper from %s and from this checkout\n", rep.base)
	baseBin, err := buildBase(work, base)
	if err != nil {
		return err
	}
	headBin, err := build(".", filepath.Join(work, "head"))
	if err != nil {
		return err
	}
	fmt.Fprintln(os.Stderr, "generating repositories")
	root := filepath.Join(work, "fixture")
	if err := makeFixture(root); err != nil {
		return err
	}
	for _, c := range cases(root) {
		fmt.Fprintf(os.Stderr, "scanning %s\n", c.name)
		r, err := measure(c, root, [2]string{baseBin, headBin}, rep.rounds)
		if err != nil {
			return err
		}
		rep.results = append(rep.results, r)
	}

	fmt.Print(rep.markdown())
	if rep.failed() {
		return errRegressed
	}
	return nil
}

var lopperSources = []string{"cmd", "internal", "go.mod", "go.sum"}

func lopperChanged(base string) (bool, error) {
	_, err := git(append([]string{"diff", "--quiet", base, "--"}, lopperSources...)...)
	if exitErr, ok := errors.AsType[*exec.ExitError](err); ok && exitErr.ExitCode() == 1 {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	untracked, err := git(append([]string{"ls-files", "--others", "--exclude-standard", "--"}, lopperSources...)...)
	return untracked != "", err
}

func buildBase(work, base string) (string, error) {
	src := filepath.Join(work, "src")
	if _, err := git("worktree", "add", "--quiet", "--detach", src, base); err != nil {
		return "", err
	}
	defer func() { _, _ = git("worktree", "remove", "--force", src) }()
	module, err := git("rev-parse", "--show-prefix")
	if err != nil {
		return "", err
	}
	return build(filepath.Join(src, module), filepath.Join(work, "base"))
}

func build(module, out string) (string, error) {
	into := out + string(filepath.Separator)
	cmd := exec.Command("go", "build", "-o", into, "./cmd/lopper")
	cmd.Dir = module
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("go build in %s: %w\n%s", module, err, out)
	}
	return filepath.Join(out, "lopper"), nil
}

type scanCase struct {
	name      string
	dir       string
	worktrees int
}

type result struct {
	name string
	git  [2]int
	time [2]time.Duration
}

func measure(c scanCase, root string, bins [2]string, rounds int) (result, error) {
	r := result{name: c.name}
	for i, bin := range bins {
		n, err := gitProcesses(bin, root, c)
		if err != nil {
			return r, err
		}
		r.git[i] = n
	}
	var times [2][]time.Duration
	for range rounds {
		for i, bin := range bins {
			d, err := scan(bin, root, c)
			if err != nil {
				return r, err
			}
			times[i] = append(times[i], d)
		}
	}
	for i := range bins {
		r.time[i] = median(times[i])
	}
	return r, nil
}

// gitProcesses relies on git writing one trace file per process when
// GIT_TRACE2_EVENT names a directory.
func gitProcesses(bin, root string, c scanCase) (int, error) {
	traces, err := os.MkdirTemp("", "lopper-bench-trace-")
	if err != nil {
		return 0, err
	}
	defer os.RemoveAll(traces)
	if _, err := scan(bin, root, c, "GIT_TRACE2_EVENT="+traces); err != nil {
		return 0, err
	}
	entries, err := os.ReadDir(traces)
	return len(entries), err
}

func scan(bin, root string, c scanCase, env ...string) (time.Duration, error) {
	cmd := exec.Command(bin, "scan", "--json", c.dir) //nolint:gosec // bin is the lopper this run built
	cmd.Env = slices.Concat(os.Environ(), gitEnv(root), []string{"LOPPER_NO_CACHE=1", "LOPPER_NO_UPDATE_CHECK=1"}, env)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	start := time.Now()
	err := cmd.Run()
	elapsed := time.Since(start)
	if err != nil {
		return 0, fmt.Errorf("%s scan %s: %w\n%s", bin, c.dir, err, stderr.Bytes())
	}
	return elapsed, checkScan(stdout.Bytes(), c.worktrees)
}

func checkScan(out []byte, want int) error {
	var records []struct {
		Path  string `json:"path"`
		Facts struct {
			Errors []string `json:"errors"`
		} `json:"facts"`
	}
	if err := json.Unmarshal(out, &records); err != nil {
		return fmt.Errorf("parse scan output: %w", err)
	}
	if len(records) != want {
		return fmt.Errorf("scan found %d worktrees, want %d", len(records), want)
	}
	for _, r := range records {
		if len(r.Facts.Errors) > 0 {
			return fmt.Errorf("%s: %s", r.Path, strings.Join(r.Facts.Errors, "; "))
		}
	}
	return nil
}

func median(times []time.Duration) time.Duration {
	sorted := slices.Sorted(slices.Values(times))
	n := len(sorted)
	if n%2 == 1 {
		return sorted[n/2]
	}
	return (sorted[n/2-1] + sorted[n/2]) / 2
}

func git(args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git %s: %w\n%s", strings.Join(args, " "), err, stderr.Bytes())
	}
	return strings.TrimSpace(string(out)), nil
}
