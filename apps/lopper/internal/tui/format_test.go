package tui

import (
	"testing"
	"time"
)

func TestFormatBytes(t *testing.T) {
	cases := []struct {
		n    int64
		want string
	}{
		{0, "0 B"},
		{999, "999 B"},
		{1000, "1.0 kB"},
		{1500, "1.5 kB"},
		{1_000_000, "1.0 MB"},
		{1_400_000_000, "1.4 GB"},
		{2_000_000_000_000, "2.0 TB"},
	}
	for _, tc := range cases {
		if got := formatBytes(tc.n); got != tc.want {
			t.Errorf("formatBytes(%d) = %q, want %q", tc.n, got, tc.want)
		}
	}
}

func TestFormatAge(t *testing.T) {
	const day = 24 * time.Hour
	cases := []struct {
		d    time.Duration
		want string
	}{
		{-time.Hour, "now"},
		{59 * time.Second, "now"},
		{time.Minute, "1m"},
		{59 * time.Minute, "59m"},
		{time.Hour, "1h"},
		{23 * time.Hour, "23h"},
		{day, "1d"},
		{29 * day, "29d"},
		{30 * day, "1mo"},
		{150 * day, "5mo"},
		{364 * day, "12mo"},
		{365 * day, "1y"},
		{3 * 365 * day, "3y"},
	}
	for _, tc := range cases {
		if got := formatAge(tc.d); got != tc.want {
			t.Errorf("formatAge(%v) = %q, want %q", tc.d, got, tc.want)
		}
	}
}
