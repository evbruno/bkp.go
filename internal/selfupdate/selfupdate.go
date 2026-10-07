// Package selfupdate replaces the installed bkp binary with the latest
// GitHub release build for the current platform.
package selfupdate

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

const (
	DefaultRepoURL    = "https://github.com/evbruno/bkp.go"
	DefaultBackupPath = "/tmp/bkp.bkp"
)

// Updater fetches release builds from RepoURL for GOOS/GOARCH.
type Updater struct {
	RepoURL string
	GOOS    string
	GOARCH  string
	Client  *http.Client
}

func (u *Updater) client() *http.Client {
	if u.Client != nil {
		return u.Client
	}
	return &http.Client{Timeout: 2 * time.Minute}
}

// AssetName is the release asset for this platform, e.g. bkp-linux-amd64.tar.gz.
func (u *Updater) AssetName() string {
	return fmt.Sprintf("bkp-%s-%s.tar.gz", u.GOOS, u.GOARCH)
}

// LatestTag resolves the latest release tag (e.g. "v0.0.6") from the
// redirect of <repo>/releases/latest, which avoids the GitHub API and its
// rate limits.
func (u *Updater) LatestTag() (string, error) {
	c := *u.client()
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

	resp, err := c.Get(u.RepoURL + "/releases/latest")
	if err != nil {
		return "", fmt.Errorf("resolving latest release: %w", err)
	}
	resp.Body.Close()

	loc := resp.Header.Get("Location")
	if resp.StatusCode < 300 || resp.StatusCode >= 400 || !strings.Contains(loc, "/releases/tag/") {
		return "", fmt.Errorf("resolving latest release: unexpected response %s (location %q)", resp.Status, loc)
	}
	return path.Base(loc), nil
}

// Download fetches tag's asset for this platform, verifies it against the
// release's checksums.txt, and returns the extracted binary.
func (u *Updater) Download(tag string) ([]byte, error) {
	asset := u.AssetName()

	sums, err := u.get(tag, "checksums.txt")
	if err != nil {
		return nil, err
	}
	want, err := findChecksum(sums, asset)
	if err != nil {
		return nil, err
	}

	archive, err := u.get(tag, asset)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(archive)
	if got := hex.EncodeToString(sum[:]); got != want {
		return nil, fmt.Errorf("checksum mismatch for %s: got %s, want %s", asset, got, want)
	}

	return extract(archive, strings.TrimSuffix(asset, ".tar.gz"))
}

func (u *Updater) get(tag, name string) ([]byte, error) {
	url := fmt.Sprintf("%s/releases/download/%s/%s", u.RepoURL, tag, name)
	resp, err := u.client().Get(url)
	if err != nil {
		return nil, fmt.Errorf("downloading %s: %w", name, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("downloading %s: %s", url, resp.Status)
	}
	return io.ReadAll(resp.Body)
}

func findChecksum(sums []byte, asset string) (string, error) {
	sc := bufio.NewScanner(bytes.NewReader(sums))
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) == 2 && strings.TrimPrefix(fields[1], "*") == asset {
			return fields[0], nil
		}
	}
	return "", fmt.Errorf("no checksum for %s in checksums.txt (unsupported platform?)", asset)
}

func extract(archive []byte, name string) ([]byte, error) {
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return nil, fmt.Errorf("reading archive: %w", err)
	}
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil, fmt.Errorf("%s not found in archive", name)
		}
		if err != nil {
			return nil, fmt.Errorf("reading archive: %w", err)
		}
		if hdr.Typeflag == tar.TypeReg && path.Base(hdr.Name) == name {
			return io.ReadAll(tr)
		}
	}
}

// Replace copies the binary at target to backupPath, then atomically swaps
// in bin at target with target's permissions (and owner, when permitted).
// If target is a symlink, the file it points to is replaced.
func Replace(target string, bin []byte, backupPath string) error {
	target, err := filepath.EvalSymlinks(target)
	if err != nil {
		return fmt.Errorf("resolving %s: %w", target, err)
	}
	info, err := os.Stat(target)
	if err != nil {
		return err
	}

	if err := copyFile(target, backupPath, info.Mode().Perm()); err != nil {
		return fmt.Errorf("backing up %s to %s: %w", target, backupPath, err)
	}

	// Stage next to target so the final rename stays on one filesystem
	// (and is therefore atomic).
	tmp, err := os.CreateTemp(filepath.Dir(target), ".bkp-update-*")
	if err != nil {
		return permissionHint(fmt.Errorf("staging new binary: %w", err))
	}
	defer os.Remove(tmp.Name())

	if _, err := tmp.Write(bin); err != nil {
		tmp.Close()
		return fmt.Errorf("staging new binary: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("staging new binary: %w", err)
	}
	if err := os.Chmod(tmp.Name(), info.Mode().Perm()); err != nil {
		return fmt.Errorf("setting permissions: %w", err)
	}
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		// Only succeeds as root (or when we already own it); otherwise the
		// new file keeps the current user, same as a manual install would.
		_ = os.Chown(tmp.Name(), int(st.Uid), int(st.Gid))
	}

	if err := os.Rename(tmp.Name(), target); err != nil {
		return permissionHint(fmt.Errorf("replacing %s: %w", target, err))
	}
	return nil
}

func copyFile(src, dst string, perm os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	// Remove first: an existing backup may belong to another user (e.g. a
	// previous sudo run), and O_TRUNC would keep its old mode.
	_ = os.Remove(dst)
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

func permissionHint(err error) error {
	if errors.Is(err, os.ErrPermission) {
		return fmt.Errorf("%w (try again with sudo)", err)
	}
	return err
}
