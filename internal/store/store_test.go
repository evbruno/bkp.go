package store

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.sqlite3")
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestOpen_MigrateIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.sqlite3")

	s1, err := Open(path)
	if err != nil {
		t.Fatalf("first Open returned error: %v", err)
	}
	s1.Close()

	s2, err := Open(path)
	if err != nil {
		t.Fatalf("second Open (re-migrate) returned error: %v", err)
	}
	defer s2.Close()
}

func TestOpen_CreatesParentDirectory(t *testing.T) {
	base := t.TempDir()
	path := filepath.Join(base, "nested", "deeper", "test.sqlite3")

	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	defer s.Close()

	if _, err := os.Stat(filepath.Dir(path)); err != nil {
		t.Fatalf("parent directory does not exist after Open: %v", err)
	}
}

func TestInsertLog_RoundTrip(t *testing.T) {
	s := openTestStore(t)

	compressed := int64(42)
	row := LogRow{
		Timestamp:      time.Now(),
		Project:        "app1",
		FilePath:       "/tmp/app1.db",
		FileSize:       1000,
		CompressedSize: &compressed,
		Status:         "ok",
		DurationMs:     123,
	}

	if err := s.InsertLog(row); err != nil {
		t.Fatalf("InsertLog returned error: %v", err)
	}

	var (
		project        string
		filePath       string
		fileSize       int64
		compressedSize int64
		status         string
		durationMs     int64
	)

	err := s.db.QueryRow(
		`SELECT project, file_path, file_size, compressed_size, status, duration_ms FROM backup_log WHERE id = 1`,
	).Scan(&project, &filePath, &fileSize, &compressedSize, &status, &durationMs)
	if err != nil {
		t.Fatalf("querying inserted row: %v", err)
	}

	if project != row.Project || filePath != row.FilePath || fileSize != row.FileSize ||
		compressedSize != compressed || status != row.Status || durationMs != row.DurationMs {
		t.Errorf("round-tripped row = %+v, want to match %+v", []any{project, filePath, fileSize, compressedSize, status, durationMs}, row)
	}
}

func TestInsertLog_ErrorRow(t *testing.T) {
	s := openTestStore(t)

	row := LogRow{
		Timestamp: time.Now(),
		Project:   "app1",
		FilePath:  "/tmp/app1.db",
		Status:    "error",
		Error:     "command failed",
	}

	if err := s.InsertLog(row); err != nil {
		t.Fatalf("InsertLog returned error: %v", err)
	}

	var errMsg string
	err := s.db.QueryRow(`SELECT error FROM backup_log WHERE id = 1`).Scan(&errMsg)
	if err != nil {
		t.Fatalf("querying inserted row: %v", err)
	}
	if errMsg != "command failed" {
		t.Errorf("error = %q, want %q", errMsg, "command failed")
	}
}

func TestLatestPerProject(t *testing.T) {
	s := openTestStore(t)

	base := time.Now()
	insert := func(project, status string, offset time.Duration) {
		t.Helper()
		if err := s.InsertLog(LogRow{
			Timestamp: base.Add(offset),
			Project:   project,
			FilePath:  "/tmp/" + project,
			FileSize:  100,
			Status:    status,
		}); err != nil {
			t.Fatalf("InsertLog(%s) returned error: %v", project, err)
		}
	}

	// app1: two rows, the second (later id) should win even though its
	// status differs, proving latest-by-id rather than latest-by-status.
	insert("app1", "error", 0)
	insert("app1", "ok", time.Minute)

	// app2: a single row.
	insert("app2", "ok", 0)

	rows, err := s.LatestPerProject()
	if err != nil {
		t.Fatalf("LatestPerProject returned error: %v", err)
	}

	if len(rows) != 2 {
		t.Fatalf("len(rows) = %d, want 2", len(rows))
	}

	// Ordered by project name.
	if rows[0].Project != "app1" || rows[0].Status != "ok" {
		t.Errorf("rows[0] = %+v, want app1/ok (the later of its two rows)", rows[0])
	}
	if rows[1].Project != "app2" || rows[1].Status != "ok" {
		t.Errorf("rows[1] = %+v, want app2/ok", rows[1])
	}
}

func TestInsertLog_SHA1RoundTrip(t *testing.T) {
	s := openTestStore(t)

	if err := s.InsertLog(LogRow{
		Timestamp: time.Now(),
		Project:   "app1",
		FilePath:  "/tmp/app1.db",
		Status:    "ok",
		SHA1:      "deadbeef",
	}); err != nil {
		t.Fatalf("InsertLog returned error: %v", err)
	}

	var sha1 string
	err := s.db.QueryRow(`SELECT sha1 FROM backup_log WHERE id = 1`).Scan(&sha1)
	if err != nil {
		t.Fatalf("querying inserted row: %v", err)
	}
	if sha1 != "deadbeef" {
		t.Errorf("sha1 = %q, want %q", sha1, "deadbeef")
	}
}

func TestLatestOKSHA1(t *testing.T) {
	s := openTestStore(t)

	if _, found, err := s.LatestOKSHA1("app1"); err != nil {
		t.Fatalf("LatestOKSHA1 returned error: %v", err)
	} else if found {
		t.Error("found = true for a project with no rows, want false")
	}

	insert := func(status, sha1 string) {
		t.Helper()
		if err := s.InsertLog(LogRow{
			Timestamp: time.Now(),
			Project:   "app1",
			FilePath:  "/tmp/app1.db",
			Status:    status,
			SHA1:      sha1,
		}); err != nil {
			t.Fatalf("InsertLog returned error: %v", err)
		}
	}

	insert("ok", "hash-v1")
	insert("error", "hash-v2") // a later, failed run should not shadow the last successful hash
	insert("ok", "")           // an ok row predating the sha1 column (or otherwise unset)

	sum, found, err := s.LatestOKSHA1("app1")
	if err != nil {
		t.Fatalf("LatestOKSHA1 returned error: %v", err)
	}
	if found {
		t.Errorf("found = true with sum %q, want false (latest ok row has no sha1 recorded)", sum)
	}
}

func TestMigrate_AddsSHA1ColumnToExistingTable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.sqlite3")

	// Simulate a pre-sha1 database: create the table without the column and
	// insert a row, bypassing Open/migrate.
	pre, err := Open(path)
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	if _, err := pre.db.Exec(`ALTER TABLE backup_log DROP COLUMN sha1`); err != nil {
		t.Fatalf("dropping sha1 column to simulate a legacy schema: %v", err)
	}
	if _, err := pre.db.Exec(
		`INSERT INTO backup_log (timestamp, project, file_path, status) VALUES (?, ?, ?, ?)`,
		time.Now().UTC().Format(time.RFC3339), "app1", "/tmp/app1.db", "ok",
	); err != nil {
		t.Fatalf("inserting into legacy schema: %v", err)
	}
	pre.Close()

	post, err := Open(path)
	if err != nil {
		t.Fatalf("re-Open (migrate legacy schema) returned error: %v", err)
	}
	defer post.Close()

	hasSHA1, err := post.hasSHA1Column()
	if err != nil {
		t.Fatalf("hasSHA1Column returned error: %v", err)
	}
	if !hasSHA1 {
		t.Error("hasSHA1Column = false after migrate, want true")
	}

	// The pre-existing row should read back with no sha1, not an error.
	_, found, err := post.LatestOKSHA1("app1")
	if err != nil {
		t.Fatalf("LatestOKSHA1 returned error: %v", err)
	}
	if found {
		t.Error("found = true for a row that predates the sha1 column, want false")
	}
}
