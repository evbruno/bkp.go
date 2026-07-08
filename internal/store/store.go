package store

import (
	"database/sql"
	"fmt"
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
}

// Store wraps the orchestrator's SQLite database.
type Store struct {
	db *sql.DB
}

// Open opens (creating if missing) the SQLite database at path and ensures
// the backup_log table exists.
func Open(path string) (*Store, error) {
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
	_, err := s.db.Exec(`
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
);`)
	if err != nil {
		return fmt.Errorf("migrating store: %w", err)
	}
	return nil
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

	_, err := s.db.Exec(
		`INSERT INTO backup_log (timestamp, project, file_path, file_size, compressed_size, status, error, duration_ms)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		row.Timestamp.UTC().Format(time.RFC3339),
		row.Project,
		row.FilePath,
		row.FileSize,
		compressedSize,
		row.Status,
		errVal,
		row.DurationMs,
	)
	if err != nil {
		return fmt.Errorf("inserting log row: %w", err)
	}
	return nil
}

// LatestPerProject returns the most recent backup_log row for every distinct
// project, ordered by project name. Read-only.
func (s *Store) LatestPerProject() ([]LogRow, error) {
	rows, err := s.db.Query(`
SELECT project, timestamp, file_path, file_size, compressed_size, status, error, duration_ms
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
		)

		if err := rows.Scan(&r.Project, &timestamp, &r.FilePath, &r.FileSize, &compressedSize, &r.Status, &errMsg, &r.DurationMs); err != nil {
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
