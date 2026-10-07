package selfupdate

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func tarGz(t *testing.T, name string, content []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(content)), Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	tw.Write(content)
	tw.Close()
	gw.Close()
	return buf.Bytes()
}

// fakeRelease serves a GitHub-like releases layout for tag, with one asset
// for linux/amd64. checksum overrides the published sha256 when non-empty.
func fakeRelease(t *testing.T, tag string, bin []byte, checksum string) *httptest.Server {
	t.Helper()
	archive := tarGz(t, "bkp-linux-amd64", bin)
	if checksum == "" {
		sum := sha256.Sum256(archive)
		checksum = hex.EncodeToString(sum[:])
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/releases/tag/"+tag, http.StatusFound)
	})
	mux.HandleFunc("/releases/download/"+tag+"/checksums.txt", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "%s  bkp-linux-amd64.tar.gz\n", checksum)
	})
	mux.HandleFunc("/releases/download/"+tag+"/bkp-linux-amd64.tar.gz", func(w http.ResponseWriter, r *http.Request) {
		w.Write(archive)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestLatestTagAndDownload(t *testing.T) {
	srv := fakeRelease(t, "v9.9.9", []byte("new binary"), "")
	u := &Updater{RepoURL: srv.URL, GOOS: "linux", GOARCH: "amd64"}

	tag, err := u.LatestTag()
	if err != nil {
		t.Fatalf("LatestTag: %v", err)
	}
	if tag != "v9.9.9" {
		t.Fatalf("tag = %q, want v9.9.9", tag)
	}

	bin, err := u.Download(tag)
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if string(bin) != "new binary" {
		t.Errorf("bin = %q, want %q", bin, "new binary")
	}
}

func TestDownload_RejectsChecksumMismatch(t *testing.T) {
	srv := fakeRelease(t, "v9.9.9", []byte("new binary"), strings.Repeat("0", 64))
	u := &Updater{RepoURL: srv.URL, GOOS: "linux", GOARCH: "amd64"}

	if _, err := u.Download("v9.9.9"); err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Errorf("Download err = %v, want checksum mismatch", err)
	}
}

func TestDownload_UnsupportedPlatform(t *testing.T) {
	srv := fakeRelease(t, "v9.9.9", []byte("new binary"), "")
	u := &Updater{RepoURL: srv.URL, GOOS: "windows", GOARCH: "386"}

	if _, err := u.Download("v9.9.9"); err == nil || !strings.Contains(err.Error(), "bkp-windows-386.tar.gz") {
		t.Errorf("Download err = %v, want missing-asset error", err)
	}
}

func TestReplace_KeepsPathAndPermsAndBacksUp(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "bkp")
	if err := os.WriteFile(target, []byte("old binary"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(target, 0o750); err != nil {
		t.Fatal(err)
	}
	backup := filepath.Join(dir, "bkp.bkp")

	if err := Replace(target, []byte("new binary"), backup); err != nil {
		t.Fatalf("Replace: %v", err)
	}

	got, _ := os.ReadFile(target)
	if string(got) != "new binary" {
		t.Errorf("target = %q, want new binary", got)
	}
	info, _ := os.Stat(target)
	if info.Mode().Perm() != 0o750 {
		t.Errorf("target mode = %o, want 750", info.Mode().Perm())
	}
	old, _ := os.ReadFile(backup)
	if string(old) != "old binary" {
		t.Errorf("backup = %q, want old binary", old)
	}

	leftovers, _ := filepath.Glob(filepath.Join(dir, ".bkp-update-*"))
	if len(leftovers) != 0 {
		t.Errorf("staging files left behind: %v", leftovers)
	}
}

func TestReplace_FollowsSymlink(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "bkp-real")
	link := filepath.Join(dir, "bkp")
	if err := os.WriteFile(real, []byte("old binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}

	if err := Replace(link, []byte("new binary"), filepath.Join(dir, "bkp.bkp")); err != nil {
		t.Fatalf("Replace: %v", err)
	}

	if fi, err := os.Lstat(link); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Errorf("symlink %s was replaced, want it kept", link)
	}
	got, _ := os.ReadFile(real)
	if string(got) != "new binary" {
		t.Errorf("symlink target = %q, want new binary", got)
	}
}
