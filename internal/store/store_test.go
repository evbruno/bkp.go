package store

import (
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
