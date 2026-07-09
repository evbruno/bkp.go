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

func TestRun_DeletesCompressedArtifactByDefault(t *testing.T) {
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
	results := Run(cfg, st, Options{})

	if results[0].Status != "ok" {
		t.Fatalf("status = %q, want ok (error=%s)", results[0].Status, results[0].Error)
	}
	if _, err := os.Stat(filepath.Join(dir, "app.db.gz")); !os.IsNotExist(err) {
		t.Errorf("gz artifact still exists after a successful run, want it deleted (keep_compressed defaults to false): err=%v", err)
	}
}

func TestRun_KeepCompressedTrueKeepsArtifact(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "app.db"), []byte("data"), 0o644); err != nil {
		t.Fatalf("writing fixture: %v", err)
	}

	timestampOff := false
	keepCompressed := true
	cfg := &config.Config{
		Title:  "test",
		Target: filepath.Join(dir, "orchestrator.sqlite3"),
		Projects: []config.Project{
			{Name: "app", BaseDir: dir, File: "app.db", Command: "true", Timestamp: &timestampOff, KeepCompressed: &keepCompressed},
		},
	}
	backupSelf := false
	cfg.BackupSelf = &backupSelf

	st := newTestStore(t)
	results := Run(cfg, st, Options{})

	if results[0].Status != "ok" {
		t.Fatalf("status = %q, want ok (error=%s)", results[0].Status, results[0].Error)
	}
	if _, err := os.Stat(filepath.Join(dir, "app.db.gz")); err != nil {
		t.Errorf("expected gz artifact to survive with keep_compressed: true: %v", err)
	}
}

func TestRun_ArtifactNotDeletedOnCommandFailure(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "app.db"), []byte("data"), 0o644); err != nil {
		t.Fatalf("writing fixture: %v", err)
	}

	timestampOff := false
	cfg := &config.Config{
		Title:  "test",
		Target: filepath.Join(dir, "orchestrator.sqlite3"),
		Projects: []config.Project{
			{Name: "app", BaseDir: dir, File: "app.db", Command: "false", Timestamp: &timestampOff},
		},
	}
	backupSelf := false
	cfg.BackupSelf = &backupSelf

	st := newTestStore(t)
	results := Run(cfg, st, Options{})

	if results[0].Status != "error" {
		t.Fatalf("status = %q, want error", results[0].Status)
	}
	if _, err := os.Stat(filepath.Join(dir, "app.db.gz")); err != nil {
		t.Errorf("expected gz artifact to survive a failed command (for debugging): %v", err)
	}
}

func TestRun_CompressAddsTimestampByDefault(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "app.db"), []byte("data"), 0o644); err != nil {
		t.Fatalf("writing fixture: %v", err)
	}

	marker := filepath.Join(dir, "artifact-name.txt")
	keepCompressed := true
	cfg := &config.Config{
		Title:  "test",
		Target: filepath.Join(dir, "orchestrator.sqlite3"),
		Projects: []config.Project{
			{Name: "app", BaseDir: dir, File: "app.db", Command: "echo {{file}} > " + marker, KeepCompressed: &keepCompressed},
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
		t.Errorf("expected gz artifact %q to exist (keep_compressed: true): %v", artifact, err)
	}
}

func TestRun_TimestampDisabledKeepsFixedName(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "app.db"), []byte("data"), 0o644); err != nil {
		t.Fatalf("writing fixture: %v", err)
	}

	timestampOff := false
	skipUnchangedOff := false
	keepCompressed := true
	cfg := &config.Config{
		Title:  "test",
		Target: filepath.Join(dir, "orchestrator.sqlite3"),
		Projects: []config.Project{
			// skip_unchanged is disabled here since this test is specifically
			// about the timestamp/overwrite behavior, not the skip feature.
			// keep_compressed is enabled so the artifact survives to be inspected.
			{Name: "app", BaseDir: dir, File: "app.db", Command: "true", Timestamp: &timestampOff, SkipUnchanged: &skipUnchangedOff, KeepCompressed: &keepCompressed},
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

func TestRun_SkipsUnchangedFileByDefault(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "ran.txt")
	if err := os.WriteFile(filepath.Join(dir, "app.db"), []byte("data"), 0o644); err != nil {
		t.Fatalf("writing fixture: %v", err)
	}

	cfg := &config.Config{
		Title:  "test",
		Target: filepath.Join(dir, "orchestrator.sqlite3"),
		Projects: []config.Project{
			// Appends one line per actual run, so a skipped run leaves the
			// line count unchanged.
			{Name: "app", BaseDir: dir, File: "app.db", Command: "echo ran >> " + marker},
		},
	}
	backupSelf := false
	cfg.BackupSelf = &backupSelf

	st := newTestStore(t)

	if results := Run(cfg, st, Options{}); results[0].Status != "ok" {
		t.Fatalf("first run status = %q, want ok (error=%s)", results[0].Status, results[0].Error)
	}
	results := Run(cfg, st, Options{})
	if results[0].Status != "skipped" {
		t.Fatalf("second run (unchanged file) status = %q, want skipped", results[0].Status)
	}
	if results[0].Error != "" {
		t.Errorf("skipped run Error = %q, want empty", results[0].Error)
	}

	got, err := os.ReadFile(marker)
	if err != nil {
		t.Fatalf("reading marker file: %v", err)
	}
	if lines := strings.Count(string(got), "ran"); lines != 1 {
		t.Errorf("command ran %d times, want exactly 1 (second run should have been skipped)", lines)
	}

	sum, found, err := st.LatestOKSHA1("app")
	if err != nil {
		t.Fatalf("LatestOKSHA1 returned error: %v", err)
	}
	if !found || sum == "" {
		t.Error("LatestOKSHA1 = (empty, false), want a recorded sha1 from the successful run")
	}
}

func TestRun_SkipUnchangedDisabled_AlwaysRuns(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "ran.txt")
	if err := os.WriteFile(filepath.Join(dir, "app.db"), []byte("data"), 0o644); err != nil {
		t.Fatalf("writing fixture: %v", err)
	}

	skipOff := false
	cfg := &config.Config{
		Title:  "test",
		Target: filepath.Join(dir, "orchestrator.sqlite3"),
		Projects: []config.Project{
			{Name: "app", BaseDir: dir, File: "app.db", Command: "echo ran >> " + marker, SkipUnchanged: &skipOff},
		},
	}
	backupSelf := false
	cfg.BackupSelf = &backupSelf

	st := newTestStore(t)

	for i := 0; i < 2; i++ {
		if results := Run(cfg, st, Options{}); results[0].Status != "ok" {
			t.Fatalf("run %d status = %q, want ok (error=%s)", i, results[0].Status, results[0].Error)
		}
	}

	got, err := os.ReadFile(marker)
	if err != nil {
		t.Fatalf("reading marker file: %v", err)
	}
	if lines := strings.Count(string(got), "ran"); lines != 2 {
		t.Errorf("command ran %d times, want exactly 2 (skip_unchanged: false should never skip)", lines)
	}
}

func TestRun_ReRunsAfterFileContentChanges(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "app.db")
	if err := os.WriteFile(src, []byte("data-v1"), 0o644); err != nil {
		t.Fatalf("writing fixture: %v", err)
	}

	cfg := &config.Config{
		Title:  "test",
		Target: filepath.Join(dir, "orchestrator.sqlite3"),
		Projects: []config.Project{
			{Name: "app", BaseDir: dir, File: "app.db", Command: "true"},
		},
	}
	backupSelf := false
	cfg.BackupSelf = &backupSelf

	st := newTestStore(t)

	if results := Run(cfg, st, Options{}); results[0].Status != "ok" {
		t.Fatalf("first run status = %q, want ok (error=%s)", results[0].Status, results[0].Error)
	}

	if err := os.WriteFile(src, []byte("data-v2"), 0o644); err != nil {
		t.Fatalf("rewriting fixture: %v", err)
	}

	results := Run(cfg, st, Options{})
	if results[0].Status != "ok" {
		t.Errorf("run after content change status = %q, want ok (content differs, should not skip)", results[0].Status)
	}
}

func TestRun_DoesNotSkipBasedOnFailedRun(t *testing.T) {
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

	if results := Run(cfg, st, Options{}); results[0].Status != "error" {
		t.Fatalf("first run status = %q, want error", results[0].Status)
	}

	// Same file content, but the only prior row is an error, not "ok" — so
	// this must actually attempt the backup again, not skip.
	cfg.Projects[0].Command = "true"
	results := Run(cfg, st, Options{})
	if results[0].Status != "ok" {
		t.Errorf("second run status = %q, want ok (a prior error shouldn't count as \"unchanged\")", results[0].Status)
	}
}

func TestRun_ResultIncludesFileNameAndSHA1(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "app.db"), []byte("data"), 0o644); err != nil {
		t.Fatalf("writing fixture: %v", err)
	}

	cfg := &config.Config{
		Title:       "test",
		Target:      filepath.Join(dir, "orchestrator.sqlite3"),
		SelfCommand: "true",
		Projects: []config.Project{
			{Name: "app", BaseDir: dir, File: "app.db", Command: "true"},
		},
	}

	st := newTestStore(t)

	// First run: "ok", should carry the file name and a real sha1.
	results := Run(cfg, st, Options{})
	if len(results) != 2 {
		t.Fatalf("len(results) = %d, want 2 (project + orchestrator)", len(results))
	}
	project, orchestrator := results[0], results[1]

	if project.Status != "ok" {
		t.Fatalf("project status = %q, want ok (error=%s)", project.Status, project.Error)
	}
	if project.FileName != "app.db" {
		t.Errorf("project.FileName = %q, want %q", project.FileName, "app.db")
	}
	if len(project.SHA1) != 40 {
		t.Errorf("project.SHA1 = %q, want a 40-char hex sha1", project.SHA1)
	}

	if orchestrator.Status != "ok" {
		t.Fatalf("orchestrator status = %q, want ok (error=%s)", orchestrator.Status, orchestrator.Error)
	}
	if orchestrator.FileName != "orchestrator.sqlite3" {
		t.Errorf("orchestrator.FileName = %q, want %q", orchestrator.FileName, "orchestrator.sqlite3")
	}
	if orchestrator.SHA1 != "" {
		t.Errorf("orchestrator.SHA1 = %q, want empty (no hash computed for the self-backup)", orchestrator.SHA1)
	}

	// Second run: "skipped", should still report the file name and the same sha1.
	skipped := Run(cfg, st, Options{})[0]
	if skipped.Status != "skipped" {
		t.Fatalf("second run status = %q, want skipped", skipped.Status)
	}
	if skipped.FileName != "app.db" {
		t.Errorf("skipped.FileName = %q, want %q", skipped.FileName, "app.db")
	}
	if skipped.SHA1 != project.SHA1 {
		t.Errorf("skipped.SHA1 = %q, want %q (same content as the first run)", skipped.SHA1, project.SHA1)
	}

	// dry-run: file name is known up front, but no hash is computed.
	dryRun := Run(cfg, st, Options{DryRun: true})[0]
	if dryRun.Status != "dry-run" {
		t.Fatalf("dry-run status = %q, want dry-run", dryRun.Status)
	}
	if dryRun.FileName != "app.db" {
		t.Errorf("dryRun.FileName = %q, want %q", dryRun.FileName, "app.db")
	}
	if dryRun.SHA1 != "" {
		t.Errorf("dryRun.SHA1 = %q, want empty (dry-run never hashes)", dryRun.SHA1)
	}

	// error: hashing still happens before the command runs, so it's reported too.
	cfg.Projects[0].Command = "false"
	cfg.Projects[0].SkipUnchanged = boolPtr(false)
	errored := Run(cfg, st, Options{})[0]
	if errored.Status != "error" {
		t.Fatalf("errored run status = %q, want error", errored.Status)
	}
	if errored.FileName != "app.db" {
		t.Errorf("errored.FileName = %q, want %q", errored.FileName, "app.db")
	}
	if len(errored.SHA1) != 40 {
		t.Errorf("errored.SHA1 = %q, want a 40-char hex sha1", errored.SHA1)
	}
}

func boolPtr(b bool) *bool { return &b }
