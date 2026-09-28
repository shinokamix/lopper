// Package update finds the latest lopper release and installs it over the
// running binary. It is the only package that reaches the network.
package update

import (
	"archive/tar"
	"archive/zip"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"golang.org/x/mod/module"
	"golang.org/x/mod/semver"
)

// Repo is where releases are published.
const Repo = "https://github.com/shinokamix/lopper"

// maxDownload bounds what is read from the network or unpacked from an
// archive; a release archive is a few megabytes.
const maxDownload = 100 << 20

// Updater installs releases of Repo over the binary at Exe.
type Updater struct {
	Repo     string
	Client   *http.Client
	Exe      string
	OS, Arch string
}

// New returns an Updater for the running binary.
func New() (*Updater, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("locate lopper binary: %w", err)
	}
	if exe, err = filepath.EvalSymlinks(exe); err != nil {
		return nil, fmt.Errorf("locate lopper binary: %w", err)
	}
	return &Updater{
		Repo:   Repo,
		Client: &http.Client{Timeout: 5 * time.Minute},
		Exe:    exe,
		OS:     runtime.GOOS,
		Arch:   runtime.GOARCH,
	}, nil
}

// Released reports whether version is that of a release. A binary built
// from source is not: it is "dev", or since Go 1.24 a pseudo-version of
// its commit such as v0.0.0-20260928234116-02fd2a7054ad, +dirty if the
// checkout had changes. Replacing it with a release would lose the build.
func Released(version string) bool {
	v := canonical(version)
	return semver.IsValid(v) && !module.IsPseudoVersion(v) && semver.Build(v) == ""
}

// Newer reports whether release is a later version than current, both
// Released. They may lack the leading v: goreleaser sets it without.
func Newer(current, release string) bool {
	return semver.Compare(canonical(release), canonical(current)) > 0
}

func canonical(v string) string {
	if !strings.HasPrefix(v, "v") {
		v = "v" + v
	}
	return v
}

// Latest returns the tag of the latest release, which GitHub tells by
// redirecting releases/latest to releases/tag/<tag>. Unlike its API, the
// redirect has no rate limit.
func (u *Updater) Latest(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, u.Repo+"/releases/latest", http.NoBody)
	if err != nil {
		return "", err
	}
	client := *u.Client
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("find latest release: %w", err)
	}
	_ = resp.Body.Close()
	loc, err := resp.Location()
	if err != nil {
		return "", fmt.Errorf("find latest release: %s", resp.Status)
	}
	dir, tag := path.Split(loc.Path)
	if !strings.HasSuffix(dir, "/releases/tag/") || !semver.IsValid(tag) {
		return "", errors.New("find latest release: none is published yet")
	}
	return tag, nil
}

// Install downloads release tag, checks it against the release's
// checksums and puts its binary in place of Exe. Exe is left as it was
// if anything fails before that.
func (u *Updater) Install(ctx context.Context, tag string) error {
	name := "lopper_" + u.OS + "_" + u.Arch + ".tar.gz"
	if u.OS == "windows" {
		name = "lopper_" + u.OS + "_" + u.Arch + ".zip"
	}
	base := u.Repo + "/releases/download/" + tag + "/"
	sums, err := u.get(ctx, base+"checksums.txt")
	if err != nil {
		return err
	}
	want, err := checksum(sums, name)
	if err != nil {
		return err
	}
	archive, err := u.get(ctx, base+name)
	if err != nil {
		return err
	}
	if got := sha256.Sum256(archive); hex.EncodeToString(got[:]) != want {
		return fmt.Errorf("%s of %s does not match its checksum", name, tag)
	}
	bin, err := unpack(archive, u.OS == "windows")
	if err != nil {
		return fmt.Errorf("%s of %s: %w", name, tag, err)
	}
	return replace(u.Exe, bin, u.OS == "windows")
}

func (u *Updater) get(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
	if err != nil {
		return nil, err
	}
	resp, err := u.Client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download %s: %s", url, resp.Status)
	}
	return readAll(resp.Body)
}

// checksum finds the SHA-256 of name in a checksums.txt, whose lines are
// "<hex>  <file name>".
func checksum(sums []byte, name string) (string, error) {
	sc := bufio.NewScanner(bytes.NewReader(sums))
	for sc.Scan() {
		if sum, file, ok := strings.Cut(sc.Text(), "  "); ok && file == name {
			return sum, nil
		}
	}
	return "", fmt.Errorf("%s is not listed in the release's checksums", name)
}

// unpack returns the lopper binary from a release archive: a zip on
// Windows, a gzipped tarball elsewhere.
func unpack(archive []byte, isZip bool) ([]byte, error) {
	if isZip {
		zr, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
		if err != nil {
			return nil, err
		}
		i := slices.IndexFunc(zr.File, func(f *zip.File) bool { return f.Name == "lopper.exe" })
		if i < 0 {
			return nil, errors.New("no lopper.exe inside")
		}
		r, err := zr.File[i].Open()
		if err != nil {
			return nil, err
		}
		defer r.Close()
		return readAll(r)
	}
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return nil, err
	}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil, errors.New("no lopper binary inside")
		}
		if err != nil {
			return nil, err
		}
		if h.Name == "lopper" && h.Typeflag == tar.TypeReg {
			return readAll(tr)
		}
	}
}

func readAll(r io.Reader) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, maxDownload+1))
	if err == nil && len(data) > maxDownload {
		err = errors.New("larger than any lopper release")
	}
	return data, err
}

// replace puts bin in place of exe by renaming a file written next to it:
// a running lopper keeps its file, and no half-written binary is left.
// Windows cannot replace or delete a running binary, but can rename it
// away first: to a name of its own each time, as the one renamed by the
// update before may still run. Those that no longer do are deleted here.
func replace(exe string, bin []byte, windows bool) error {
	dir := filepath.Dir(exe)
	tmp, err := os.CreateTemp(dir, ".lopper-update-*")
	if err != nil {
		return fmt.Errorf("cannot write to %s: %w", dir, err)
	}
	defer os.Remove(tmp.Name()) // fails harmlessly once renamed
	if _, err := tmp.Write(bin); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o755); err != nil { //nolint:gosec // G302: an executable must be executable
		return err
	}
	if windows {
		if stale, err := filepath.Glob(filepath.Join(dir, ".lopper-*.old")); err == nil {
			for _, f := range stale {
				_ = os.Remove(f)
			}
		}
		old := filepath.Join(dir, ".lopper-"+rand.Text()+".old")
		if err := os.Rename(exe, old); err != nil {
			return err
		}
		if err := os.Rename(tmp.Name(), exe); err != nil {
			_ = os.Rename(old, exe)
			return err
		}
		return nil
	}
	return os.Rename(tmp.Name(), exe)
}
