package tui

import "testing"

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
		if got := formatBytes(&tc.n); got != tc.want {
			t.Errorf("formatBytes(%d) = %q, want %q", tc.n, got, tc.want)
		}
	}
	if got := formatBytes(nil); got != "…" {
		t.Errorf("formatBytes(nil) = %q, want placeholder", got)
	}
}
