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
	"io/fs"
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

// maxDownload caps what lopper downloads or unpacks. A release archive is a
// few megabytes.
const maxDownload = 100 << 20

// Updater installs releases of Repo over the binary at Exe.
type Updater struct {
	Repo     string
	Client   *http.Client
	Exe      string
	OS, Arch string
	// Cache is the directory where Refresh, Skip and Postpone keep what they
	// learn. "" keeps nothing.
	Cache string
}

// checkEvery is how long Refresh trusts the latest release it found,
// retryEvery how long it waits after failing to find one, and
// postponeFor how long Postpone keeps a release from being offered.
const (
	checkEvery  = 24 * time.Hour
	retryEvery  = time.Hour
	postponeFor = 24 * time.Hour
)

// New returns an Updater for the running binary.
func New() (*Updater, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("locate lopper binary: %w", err)
	}
	if exe, err = filepath.EvalSymlinks(exe); err != nil {
		return nil, fmt.Errorf("locate lopper binary: %w", err)
	}
	u := &Updater{
		Repo:   Repo,
		Client: &http.Client{Timeout: 5 * time.Minute},
		Exe:    exe,
		OS:     runtime.GOOS,
		Arch:   runtime.GOARCH,
	}
	if dir, err := os.UserCacheDir(); err == nil {
		u.Cache = filepath.Join(dir, "lopper")
	}
	return u, nil
}

// Available returns the latest release the last Refresh found if it is
// newer than current, not skipped and not postponed, and "" otherwise.
// It reads only the cache, so starting lopper never waits for the network.
func (u *Updater) Available(current string) string {
	latest, _ := u.read(latestFile)
	if latest == "" || !Newer(current, latest) {
		return ""
	}
	if skipped, _ := u.read(skippedFile); skipped == latest {
		return ""
	}
	if postponed, when := u.read(postponedFile); postponed == latest && time.Since(when) < postponeFor {
		return ""
	}
	return latest
}

// Refresh asks GitHub for the latest release, which Available offers on the
// next start. It asks at most once a day after an answer and once an hour
// after a failure, so an offline start does not ask every time. It keeps
// nothing if ctx ends first.
func (u *Updater) Refresh(ctx context.Context) {
	if u.Cache == "" {
		return
	}
	latest, asked := u.read(latestFile)
	wait := checkEvery
	if latest == "" {
		wait = retryEvery
	}
	if time.Since(asked) < wait {
		return
	}
	ask, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	latest, err := u.Latest(ask)
	if err != nil && ctx.Err() != nil {
		return // lopper quit, so ask on the next start
	}
	u.write(latestFile, latest) // "" if offline
}

// Skip keeps Available from offering release tag again.
func (u *Updater) Skip(tag string) {
	u.write(skippedFile, tag)
}

// Postpone keeps Available from offering release tag for a day.
func (u *Updater) Postpone(tag string) {
	u.write(postponedFile, tag)
}

// Files in Cache.
const (
	latestFile    = "latest-release"    // what Refresh found, "" if it failed
	skippedFile   = "skipped-release"   // what Skip was told
	postponedFile = "postponed-release" // what Postpone was told, and when
)

// read returns the release kept in a file in Cache, if any, and when it
// was written.
func (u *Updater) read(name string) (tag string, written time.Time) {
	if u.Cache == "" {
		return "", time.Time{}
	}
	f := filepath.Join(u.Cache, name)
	info, err := os.Stat(f)
	if err != nil {
		return "", time.Time{}
	}
	data, err := os.ReadFile(f)
	if tag := strings.TrimSpace(string(data)); err == nil && semver.IsValid(tag) {
		return tag, info.ModTime()
	}
	return "", info.ModTime()
}

// write keeps tag in a file in Cache. A failed write only means asking again.
func (u *Updater) write(name, tag string) {
	if u.Cache == "" {
		return
	}
	if err := os.MkdirAll(u.Cache, 0o700); err == nil {
		_ = os.WriteFile(filepath.Join(u.Cache, name), []byte(tag+"\n"), 0o600)
	}
}

// Released reports whether version names a release rather than "dev",
// a pseudo-version or a dirty build. The caller checks build provenance,
// since a clean local build on a git tag has the same version as a release.
func Released(version string) bool {
	v := canonical(version)
	return semver.IsValid(v) && !module.IsPseudoVersion(v) && semver.Build(v) == ""
}

// Newer reports whether release is later than current. Both must be
// Released, and may lack the leading v, which goreleaser omits.
func Newer(current, release string) bool {
	return semver.Compare(canonical(release), canonical(current)) > 0
}

func canonical(v string) string {
	if !strings.HasPrefix(v, "v") {
		v = "v" + v
	}
	return v
}

// Latest returns the tag of the latest release from GitHub's redirect of
// releases/latest to releases/tag/<tag>. Unlike the API, the redirect has no
// rate limit.
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
// checksums and puts its binary in place of Exe. If anything fails before
// that, Exe stays as it was.
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
	// Nothing stops replace once it starts, since on Windows that could
	// leave no binary.
	if err := ctx.Err(); err != nil {
		return err
	}
	err = replace(u.Exe, bin, u.OS == "windows")
	if errors.Is(err, fs.ErrPermission) {
		if u.OS == "windows" {
			return fmt.Errorf("%w; run lopper update in a terminal opened as administrator", err)
		}
		return fmt.Errorf("%w; run sudo lopper update", err)
	}
	return err
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

// replace writes bin next to exe and renames it over exe, so a running
// lopper keeps its file and no half-written binary is left. Windows cannot
// replace or delete a running binary but can rename it away. Each update
// renames it to a new name, since the one the previous update renamed may
// still run, and deletes the old ones that no longer run.
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
