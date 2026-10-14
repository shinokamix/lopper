package lopper

import "testing"

func TestFormatSize(t *testing.T) {
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
		if got := FormatSize(tc.n); got != tc.want {
			t.Errorf("FormatSize(%d) = %q, want %q", tc.n, got, tc.want)
		}
	}
}

func TestQuote(t *testing.T) {
	for path, want := range map[string]string{
		"/tmp/wt-a_1.2": "/tmp/wt-a_1.2",
		"/tmp/my wt":    "'/tmp/my wt'",
		"/tmp/it's":     `'/tmp/it'\''s'`,
		"/tmp/$HOME":    "'/tmp/$HOME'",
		"":              "''",
	} {
		if got := Quote(path); got != want {
			t.Errorf("Quote(%q) = %s, want %s", path, got, want)
		}
	}
}
