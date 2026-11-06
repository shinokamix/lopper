package main

import (
	"testing"
	"time"
)

func TestReportFails(t *testing.T) {
	ms := time.Millisecond
	for _, tc := range []struct {
		name        string
		git         [2]int
		time        [2]time.Duration
		maxSlowdown float64
		want        bool
	}{
		{"same", [2]int{10, 10}, [2]time.Duration{100 * ms, 100 * ms}, 20, false},
		{"one more git process", [2]int{10, 11}, [2]time.Duration{100 * ms, 100 * ms}, 20, true},
		{"fewer git processes", [2]int{10, 9}, [2]time.Duration{100 * ms, 100 * ms}, 20, false},
		{"slower within the limit", [2]int{10, 10}, [2]time.Duration{100 * ms, 119 * ms}, 20, false},
		{"slower past the limit", [2]int{10, 10}, [2]time.Duration{100 * ms, 121 * ms}, 20, true},
		{"slower with time unchecked", [2]int{10, 10}, [2]time.Duration{100 * ms, 300 * ms}, 0, false},
		{"more git with time unchecked", [2]int{10, 11}, [2]time.Duration{100 * ms, 100 * ms}, 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rep := report{maxSlowdown: tc.maxSlowdown, results: []result{{name: "worktrees", git: tc.git, time: tc.time}}}
			if got := rep.failed(); got != tc.want {
				t.Errorf("failed() = %v, want %v\n%s", got, tc.want, rep.markdown())
			}
		})
	}
}
