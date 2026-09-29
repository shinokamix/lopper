package update

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// release serves fixed responses by URL path, as GitHub serves a release.
type release map[string]reply

type reply struct {
	status   int
	location string
	data     []byte
}

func (r release) RoundTrip(req *http.Request) (*http.Response, error) {
	rep, ok := r[req.URL.Path]
	if !ok {
		rep = reply{status: http.StatusNotFound}
	}
	resp := &http.Response{
		StatusCode: rep.status,
		Status:     http.StatusText(rep.status),
		Header:     http.Header{},
		Body:       io.NopCloser(bytes.NewReader(rep.data)),
		Request:    req,
	}
	if rep.location != "" {
		resp.Header.Set("Location", rep.location)
	}
	return resp, nil
}

func file(data []byte) reply { return reply{status: http.StatusOK, data: data} }

func redirect(to string) reply { return reply{status: http.StatusFound, location: to} }

func updater(t *testing.T, goos string, r release) *Updater {
	t.Helper()
	exe := filepath.Join(t.TempDir(), "lopper")
	if err := os.WriteFile(exe, []byte("old binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	return &Updater{Repo: "https://github.com/shinokamix/lopper", Client: &http.Client{Transport: r}, Exe: exe, OS: goos, Arch: "arm64"}
}

func tarball(t *testing.T, content string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, f := range []struct{ name, content string }{{"README.md", "readme"}, {"lopper", content}} {
		if err := tw.WriteHeader(&tar.Header{Name: f.name, Mode: 0o755, Size: int64(len(f.content)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(f.content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func zipfile(t *testing.T, name, content string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create(name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte(content)); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func sums(files map[string][]byte) []byte {
	var b strings.Builder
	for name, data := range files {
		sum := sha256.Sum256(data)
		b.WriteString(hex.EncodeToString(sum[:]) + "  " + name + "\n")
	}
	return []byte(b.String())
}

func readExe(t *testing.T, u *Updater) string {
	t.Helper()
	data, err := os.ReadFile(u.Exe)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestInstallReplacesBinaryWithTheOneForThisPlatform(t *testing.T) {
	const dl = "/shinokamix/lopper/releases/download/v0.2.0/"
	for _, tc := range []struct {
		goos, archive string
		data          func(t *testing.T) []byte
	}{
		{"darwin", "lopper_darwin_arm64.tar.gz", func(t *testing.T) []byte { return tarball(t, "new binary") }},
		{"windows", "lopper_windows_arm64.zip", func(t *testing.T) []byte { return zipfile(t, "lopper.exe", "new binary") }},
	} {
		t.Run(tc.goos, func(t *testing.T) {
			archive := tc.data(t)
			other := tarball(t, "binary for another platform")
			u := updater(t, tc.goos, release{
				dl + tc.archive:                  file(archive),
				dl + "lopper_linux_amd64.tar.gz": file(other),
				dl + "checksums.txt":             file(sums(map[string][]byte{tc.archive: archive, "lopper_linux_amd64.tar.gz": other})),
			})
			if err := u.Install(context.Background(), "v0.2.0"); err != nil {
				t.Fatal(err)
			}
			if got := readExe(t, u); got != "new binary" {
				t.Errorf("binary after update = %q, want the release's", got)
			}
			if runtime.GOOS != "windows" { // no permission bits there
				if info, err := os.Stat(u.Exe); err != nil || info.Mode().Perm()&0o111 == 0 {
					t.Errorf("updated binary is not executable: %v %v", info.Mode(), err)
				}
			}
			entries, _ := os.ReadDir(filepath.Dir(u.Exe))
			for _, e := range entries {
				if strings.HasPrefix(e.Name(), ".lopper-update-") {
					t.Errorf("temporary file %s left next to the binary", e.Name())
				}
			}
		})
	}
}

// On Windows, an update renames the running lopper.exe away. The next
// update must succeed while that one still runs: then its file can be
// neither deleted nor replaced. Holding it open stands in for running it
// on Windows; elsewhere nothing stops either, and the test only shows
// that repeated updates work.
func TestWindowsUpdatesAgainWhileTheReplacedOneRuns(t *testing.T) {
	const dl = "/shinokamix/lopper/releases/download/v0.2.0/"
	archive := zipfile(t, "lopper.exe", "new binary")
	u := updater(t, "windows", release{
		dl + "lopper_windows_arm64.zip": file(archive),
		dl + "checksums.txt":            file(sums(map[string][]byte{"lopper_windows_arm64.zip": archive})),
	})
	for i := range 3 {
		if err := u.Install(context.Background(), "v0.2.0"); err != nil {
			t.Fatalf("update %d: %v", i+1, err)
		}
		renamed, _ := filepath.Glob(filepath.Join(filepath.Dir(u.Exe), ".lopper-*.old"))
		for _, f := range renamed {
			running, err := os.Open(f)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = running.Close() })
		}
	}
	if got := readExe(t, u); got != "new binary" {
		t.Errorf("binary after updates = %q, want the release's", got)
	}
}

func TestInstallKeepsBinaryWhenDownloadIsNotTheReleased(t *testing.T) {
	const dl = "/shinokamix/lopper/releases/download/v0.2.0/"
	released := tarball(t, "new binary")
	for name, r := range map[string]release{
		"tampered archive": {
			dl + "lopper_darwin_arm64.tar.gz": file(tarball(t, "malicious binary")),
			dl + "checksums.txt":              file(sums(map[string][]byte{"lopper_darwin_arm64.tar.gz": released})),
		},
		"archive not in checksums": {
			dl + "lopper_darwin_arm64.tar.gz": file(released),
			dl + "checksums.txt":              file(sums(map[string][]byte{"lopper_linux_amd64.tar.gz": released})),
		},
		"no checksums": {
			dl + "lopper_darwin_arm64.tar.gz": file(released),
		},
	} {
		t.Run(name, func(t *testing.T) {
			u := updater(t, "darwin", r)
			if err := u.Install(context.Background(), "v0.2.0"); err == nil {
				t.Error("Install succeeded")
			}
			if got := readExe(t, u); got != "old binary" {
				t.Errorf("binary = %q, want it untouched", got)
			}
		})
	}
}

func TestLatestFollowsReleasesLatestRedirect(t *testing.T) {
	u := updater(t, "darwin", release{
		"/shinokamix/lopper/releases/latest": redirect("https://github.com/shinokamix/lopper/releases/tag/v0.2.0"),
	})
	if tag, err := u.Latest(context.Background()); err != nil || tag != "v0.2.0" {
		t.Errorf("Latest = %q, %v; want v0.2.0", tag, err)
	}

	// With nothing published, GitHub redirects to the release list.
	u = updater(t, "darwin", release{
		"/shinokamix/lopper/releases/latest": redirect("https://github.com/shinokamix/lopper/releases"),
	})
	if tag, err := u.Latest(context.Background()); err == nil {
		t.Errorf("Latest = %q with no release published, want an error", tag)
	}
}

func TestNewer(t *testing.T) {
	for _, tc := range []struct {
		current, release string
		want             bool
	}{
		{"0.1.0", "v0.2.0", true}, // goreleaser sets the version without v
		{"0.2.0", "v0.2.0", false},
		{"v0.9.0", "v0.10.0", true}, // compared as numbers, not strings
		{"v0.2.0", "v0.1.0", false},
	} {
		if got := Newer(tc.current, tc.release); got != tc.want {
			t.Errorf("Newer(%q, %q) = %v, want %v", tc.current, tc.release, got, tc.want)
		}
	}
}

// A binary built from source must never be offered a release in its
// place: that would silently throw away the build.
func TestReleased(t *testing.T) {
	for v, want := range map[string]bool{
		"0.1.0":                                true, // set by goreleaser
		"v0.1.0":                               true, // go install …@v0.1.0
		"dev":                                  false,
		"v0.0.0-20260928234116-02fd2a7054ad":   false, // go build in a checkout
		"v0.1.1-0.20260928234116-02fd2a7054ad": false, // the same, after v0.1.0
		"v0.1.1-0.20260928234116-02fd2a7054ad+dirty": false,
	} {
		if got := Released(v); got != want {
			t.Errorf("Released(%q) = %v, want %v", v, got, want)
		}
	}
}
