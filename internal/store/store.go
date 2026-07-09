package store

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

// LogRow is a single backup_log entry.
type LogRow struct {
	Timestamp      time.Time
	Project        string
	FilePath       string
	FileSize       int64
	CompressedSize *int64
	Status         string
	Error          string
	DurationMs     int64
	SHA1           string // sha1 of the uncompressed source file, empty if not recorded
}

// Store wraps the orchestrator's SQLite database.
type Store struct {
	db *sql.DB
}

// Open opens (creating if missing) the SQLite database at path and ensures
// the backup_log table exists.
func Open(path string) (*Store, error) {
	if dir := filepath.Dir(path); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("creating store directory: %w", err)
		}
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("opening store: %w", err)
	}

	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}

	return s, nil
}

func (s *Store) migrate() error {
	if _, err := s.db.Exec(`
CREATE TABLE IF NOT EXISTS backup_log (
  id                 INTEGER PRIMARY KEY AUTOINCREMENT,
  timestamp          TEXT    NOT NULL,
  project            TEXT    NOT NULL,
  file_path          TEXT    NOT NULL,
  file_size          INTEGER,
  compressed_size    INTEGER,
  status             TEXT    NOT NULL,
  error              TEXT,
  duration_ms        INTEGER
);`); err != nil {
		return fmt.Errorf("migrating store: %w", err)
	}

	hasSHA1, err := s.hasSHA1Column()
	if err != nil {
		return fmt.Errorf("migrating store: %w", err)
	}
	if !hasSHA1 {
		if _, err := s.db.Exec(`ALTER TABLE backup_log ADD COLUMN sha1 TEXT`); err != nil {
			return fmt.Errorf("migrating store: adding sha1 column: %w", err)
		}
	}

	return nil
}

func (s *Store) hasSHA1Column() (bool, error) {
	rows, err := s.db.Query(`PRAGMA table_info(backup_log)`)
	if err != nil {
		return false, err
	}
	defer rows.Close()

	for rows.Next() {
		var (
			cid, notnull, pk int
			name, colType    string
			dflt             sql.NullString
		)
		if err := rows.Scan(&cid, &name, &colType, &notnull, &dflt, &pk); err != nil {
			return false, err
		}
		if name == "sha1" {
			return true, nil
		}
	}

	return false, rows.Err()
}

// InsertLog writes a single backup_log row.
func (s *Store) InsertLog(row LogRow) error {
	var compressedSize any
	if row.CompressedSize != nil {
		compressedSize = *row.CompressedSize
	}

	var errVal any
	if row.Error != "" {
		errVal = row.Error
	}

	var sha1Val any
	if row.SHA1 != "" {
		sha1Val = row.SHA1
	}

	_, err := s.db.Exec(
		`INSERT INTO backup_log (timestamp, project, file_path, file_size, compressed_size, status, error, duration_ms, sha1)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		row.Timestamp.UTC().Format(time.RFC3339),
		row.Project,
		row.FilePath,
		row.FileSize,
		compressedSize,
		row.Status,
		errVal,
		row.DurationMs,
		sha1Val,
	)
	if err != nil {
		return fmt.Errorf("inserting log row: %w", err)
	}
	return nil
}

// LatestOKSHA1 returns the sha1 recorded on the most recent successful ("ok")
// backup_log row for project, if any. found is false if the project has
// never completed a successful backup (or that row predates the sha1
// column). Read-only.
func (s *Store) LatestOKSHA1(project string) (sum string, found bool, err error) {
	var val sql.NullString
	err = s.db.QueryRow(
		`SELECT sha1 FROM backup_log WHERE project = ? AND status = 'ok' ORDER BY id DESC LIMIT 1`,
		project,
	).Scan(&val)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("querying latest sha1 for %q: %w", project, err)
	}
	if !val.Valid || val.String == "" {
		return "", false, nil
	}
	return val.String, true, nil
}

// LatestPerProject returns the most recent backup_log row for every distinct
// project, ordered by project name. Read-only.
func (s *Store) LatestPerProject() ([]LogRow, error) {
	rows, err := s.db.Query(`
SELECT project, timestamp, file_path, file_size, compressed_size, status, error, duration_ms, sha1
FROM (
  SELECT *, ROW_NUMBER() OVER (PARTITION BY project ORDER BY id DESC) AS rn
  FROM backup_log
)
WHERE rn = 1
ORDER BY project;`)
	if err != nil {
		return nil, fmt.Errorf("querying latest per project: %w", err)
	}
	defer rows.Close()

	var result []LogRow
	for rows.Next() {
		var (
			r              LogRow
			timestamp      string
			compressedSize sql.NullInt64
			errMsg         sql.NullString
			sha1Val        sql.NullString
		)

		if err := rows.Scan(&r.Project, &timestamp, &r.FilePath, &r.FileSize, &compressedSize, &r.Status, &errMsg, &r.DurationMs, &sha1Val); err != nil {
			return nil, fmt.Errorf("scanning latest row: %w", err)
		}

		r.Timestamp, err = time.Parse(time.RFC3339, timestamp)
		if err != nil {
			return nil, fmt.Errorf("parsing timestamp: %w", err)
		}
		if compressedSize.Valid {
			r.CompressedSize = &compressedSize.Int64
		}
		if errMsg.Valid {
			r.Error = errMsg.String
		}
		if sha1Val.Valid {
			r.SHA1 = sha1Val.String
		}

		result = append(result, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reading latest rows: %w", err)
	}

	return result, nil
}

// Close closes the underlying database handle.
func (s *Store) Close() error {
	return s.db.Close()
}
