package runner

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/evbruno/bkp.go/internal/config"
	"github.com/evbruno/bkp.go/internal/store"
)

func TestIsoTimestamp(t *testing.T) {
	ts := time.Date(2026, 7, 8, 19, 51, 49, 0, time.UTC)
	got := isoTimestamp(ts)
	want := "20260708T195149Z"
	if got != want {
		t.Errorf("isoTimestamp(%v) = %q, want %q", ts, got, want)
	}
}

func TestSubstitute(t *testing.T) {
	cases := []struct {
		name    string
		command string
		want    string
	}{
		{"with placeholder", "rclone copy {{file}} remote:bucket", "rclone copy app1.db.gz remote:bucket"},
		{"no placeholder runs verbatim", "rclone copy app1.db remote:bucket", "rclone copy app1.db remote:bucket"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := substitute(tc.command, "app1.db.gz")
			if got != tc.want {
				t.Errorf("substitute(%q) = %q, want %q", tc.command, got, tc.want)
			}
		})
	}
}

func TestGzipFile_SizeMath(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "source.txt")
	content := []byte("hello world, this compresses reasonably well when repeated. " +
		"hello world, this compresses reasonably well when repeated. " +
		"hello world, this compresses reasonably well when repeated.")

	if err := os.WriteFile(src, content, 0o644); err != nil {
		t.Fatalf("writing source file: %v", err)
	}

	dst := filepath.Join(dir, "source.txt.gz")
	size, err := gzipFile(src, dst)
	if err != nil {
		t.Fatalf("gzipFile returned error: %v", err)
	}

	info, err := os.Stat(dst)
	if err != nil {
		t.Fatalf("stat gz output: %v", err)
	}

	if size != info.Size() {
		t.Errorf("returned size = %d, want %d (actual file size)", size, info.Size())
	}
	if size == 0 {
		t.Error("returned size = 0, want > 0")
	}
	if size >= int64(len(content)) {
		t.Errorf("gz size = %d, want smaller than original %d for repetitive content", size, len(content))
	}
}

func newTestStore(t *testing.T) *store.Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "orchestrator.sqlite3")
	s, err := store.Open(path)
	if err != nil {
		t.Fatalf("store.Open returned error: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestRun_FailureIsolation(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"good.db", "bad.db"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("data"), 0o644); err != nil {
			t.Fatalf("writing fixture %s: %v", name, err)
		}
	}

	cfg := &config.Config{
		Title:  "test",
		Target: filepath.Join(dir, "orchestrator.sqlite3"),
		Projects: []config.Project{
			{Name: "good", BaseDir: dir, File: "good.db", Command: "true"},
			{Name: "bad", BaseDir: dir, File: "bad.db", Command: "false"},
		},
	}
	backupSelf := false
	cfg.BackupSelf = &backupSelf

	st := newTestStore(t)
	results := Run(cfg, st, Options{})

	if len(results) != 2 {
		t.Fatalf("len(results) = %d, want 2", len(results))
	}

	if results[0].Status != "ok" {
		t.Errorf("good project status = %q, want %q", results[0].Status, "ok")
	}
	if results[1].Status != "error" {
		t.Errorf("bad project status = %q, want %q", results[1].Status, "error")
	}
	if results[1].Error == "" {
		t.Error("bad project Error is empty, want a failure message")
	}
}

func TestRun_DryRunSkipsExecution(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "app.db"), []byte("data"), 0o644); err != nil {
		t.Fatalf("writing fixture: %v", err)
	}

	cfg := &config.Config{
		Title:  "test",
		Target: filepath.Join(dir, "orchestrator.sqlite3"),
		Projects: []config.Project{
			{Name: "app", BaseDir: dir, File: "app.db", Command: "false"},
		},
	}
	backupSelf := false
	cfg.BackupSelf = &backupSelf

	st := newTestStore(t)
	results := Run(cfg, st, Options{DryRun: true})

	if len(results) != 1 {
		t.Fatalf("len(results) = %d, want 1", len(results))
	}
	if results[0].Status != "dry-run" {
		t.Errorf("status = %q, want %q", results[0].Status, "dry-run")
	}

	if _, err := os.Stat(filepath.Join(dir, "app.db.gz")); err == nil {
		t.Error("gz artifact was created during dry-run, want no side effects")
	}
}

func TestRun_CompressAddsTimestampByDefault(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "app.db"), []byte("data"), 0o644); err != nil {
		t.Fatalf("writing fixture: %v", err)
	}

	marker := filepath.Join(dir, "artifact-name.txt")
	cfg := &config.Config{
		Title:  "test",
		Target: filepath.Join(dir, "orchestrator.sqlite3"),
		Projects: []config.Project{
			{Name: "app", BaseDir: dir, File: "app.db", Command: "echo {{file}} > " + marker},
		},
	}
	backupSelf := false
	cfg.BackupSelf = &backupSelf

	st := newTestStore(t)
	results := Run(cfg, st, Options{})

	if results[0].Status != "ok" {
		t.Fatalf("status = %q, want ok (error=%s)", results[0].Status, results[0].Error)
	}

	got, err := os.ReadFile(marker)
	if err != nil {
		t.Fatalf("reading marker file: %v", err)
	}
	artifact := strings.TrimSpace(string(got))

	if !regexp.MustCompile(`^app\.db\.\d{8}T\d{6}Z\.gz$`).MatchString(artifact) {
		t.Errorf("artifact = %q, want to match app.db.<ISO8601>.gz", artifact)
	}
	if _, err := os.Stat(filepath.Join(dir, artifact)); err != nil {
		t.Errorf("expected gz artifact %q to exist: %v", artifact, err)
	}
}

func TestRun_TimestampDisabledKeepsFixedName(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "app.db"), []byte("data"), 0o644); err != nil {
		t.Fatalf("writing fixture: %v", err)
	}

	timestampOff := false
	cfg := &config.Config{
		Title:  "test",
		Target: filepath.Join(dir, "orchestrator.sqlite3"),
		Projects: []config.Project{
			{Name: "app", BaseDir: dir, File: "app.db", Command: "true", Timestamp: &timestampOff},
		},
	}
	backupSelf := false
	cfg.BackupSelf = &backupSelf

	st := newTestStore(t)

	if results := Run(cfg, st, Options{}); results[0].Status != "ok" {
		t.Fatalf("first run status = %q, want ok (error=%s)", results[0].Status, results[0].Error)
	}
	if results := Run(cfg, st, Options{}); results[0].Status != "ok" {
		t.Fatalf("second run status = %q, want ok (error=%s)", results[0].Status, results[0].Error)
	}

	if _, err := os.Stat(filepath.Join(dir, "app.db.gz")); err != nil {
		t.Errorf("expected fixed-name gz artifact app.db.gz to exist: %v", err)
	}

	matches, err := filepath.Glob(filepath.Join(dir, "app.db*.gz"))
	if err != nil {
		t.Fatalf("glob error: %v", err)
	}
	if len(matches) != 1 {
		t.Errorf("glob app.db*.gz = %v, want exactly 1 file (overwritten, not duplicated)", matches)
	}
}
