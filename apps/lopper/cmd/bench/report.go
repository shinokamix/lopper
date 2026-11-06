package main

import (
	"fmt"
	"strings"
)

type report struct {
	runner      string
	base        string
	rounds      int
	maxSlowdown float64
	results     []result
}

func (r result) moreGit() bool { return r.git[1] > r.git[0] }

func (r result) slowdown() float64 {
	return 100 * (float64(r.time[1])/float64(r.time[0]) - 1)
}

func (rep report) tooSlow(r result) bool {
	return rep.maxSlowdown > 0 && r.slowdown() > rep.maxSlowdown
}

func (rep report) failed() bool {
	for _, r := range rep.results {
		if r.moreGit() || rep.tooSlow(r) {
			return true
		}
	}
	return false
}

func (rep report) markdown() string {
	var b strings.Builder
	title := "Scan speed"
	if rep.runner != "" {
		title += " on " + rep.runner
	}
	fmt.Fprintf(&b, "### %s\n\n%s\n\n", title, rep.verdict())
	fmt.Fprintf(&b, "| | base %s | this change | Change |\n|---|--:|--:|---|\n", rep.base)
	for _, r := range rep.results {
		git := "no change"
		if r.git[1] != r.git[0] {
			git = fmt.Sprintf("%+d", r.git[1]-r.git[0])
		}
		if r.moreGit() {
			git += " :x:"
		}
		time := fmt.Sprintf("%+.0f%%", r.slowdown())
		if rep.tooSlow(r) {
			time += " :x:"
		}
		fmt.Fprintf(&b, "| %s: git processes | %d | %d | %s |\n", r.name, r.git[0], r.git[1], git)
		fmt.Fprintf(&b, "| %s: time | %d ms | %d ms | %s |\n", r.name, r.time[0].Milliseconds(), r.time[1].Milliseconds(), time)
	}
	fmt.Fprintf(&b, "\nTime is the median of %d runs each.\n", rep.rounds)
	return b.String()
}

func (rep report) verdict() string {
	var moreGit, tooSlow bool
	for _, r := range rep.results {
		moreGit = moreGit || r.moreGit()
		tooSlow = tooSlow || rep.tooSlow(r)
	}
	limit := fmt.Sprintf("more than %.0f%% slower", rep.maxSlowdown)
	switch {
	case moreGit && tooSlow:
		return ":x: This change starts more git processes, and makes a scan " + limit + "."
	case moreGit:
		return ":x: This change starts more git processes."
	case tooSlow:
		return ":x: This change makes a scan " + limit + "."
	case rep.maxSlowdown > 0:
		return ":white_check_mark: No more git processes, and no scan " + limit + "."
	default:
		return ":white_check_mark: No more git processes. Time is not checked on this runner."
	}
}
