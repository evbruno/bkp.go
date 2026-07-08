package config

import (
	"os"
	"path/filepath"
	"testing"
)

func writeYAML(t *testing.T, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("writing test yaml: %v", err)
	}
	return path
}

func TestLoad_Valid(t *testing.T) {
	path := writeYAML(t, `
title: Test backup
target: /tmp/backups.sqlite3
backup_self: true
self_command: echo self

projects:
  - name: app1
    base_dir: /tmp
    file: app1.db
    command: echo {{file}}
`)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}

	if cfg.Title != "Test backup" {
		t.Errorf("Title = %q, want %q", cfg.Title, "Test backup")
	}
	if len(cfg.Projects) != 1 {
		t.Fatalf("len(Projects) = %d, want 1", len(cfg.Projects))
	}
	if !cfg.Projects[0].CompressEnabled() {
		t.Error("Projects[0].CompressEnabled() = false, want true (default)")
	}
}

func TestLoad_Defaults(t *testing.T) {
	path := writeYAML(t, `
target: /tmp/backups.sqlite3
self_command: echo self

projects:
  - name: app1
    base_dir: /tmp
    file: app1.db
    command: echo {{file}}
`)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load returned unexpected error: %v", err)
	}

	if !cfg.BackupSelfEnabled() {
		t.Error("BackupSelfEnabled() = false, want true (default, since backup_self is omitted)")
	}
	if !cfg.Projects[0].CompressEnabled() {
		t.Error("CompressEnabled() = false, want true (default, since compress is omitted)")
	}
}

func TestLoad_MissingTarget(t *testing.T) {
	path := writeYAML(t, `
projects:
  - name: app1
    base_dir: /tmp
    file: app1.db
    command: echo {{file}}
`)

	if _, err := Load(path); err == nil {
		t.Fatal("Load returned nil error, want error for missing target")
	}
}

func TestLoad_NoProjects(t *testing.T) {
	path := writeYAML(t, `
target: /tmp/backups.sqlite3
`)

	if _, err := Load(path); err == nil {
		t.Fatal("Load returned nil error, want error for empty projects")
	}
}

func TestLoad_MissingProjectField(t *testing.T) {
	path := writeYAML(t, `
target: /tmp/backups.sqlite3
projects:
  - name: app1
    base_dir: /tmp
    command: echo {{file}}
`)

	if _, err := Load(path); err == nil {
		t.Fatal("Load returned nil error, want error for missing file field")
	}
}

func TestLoad_DuplicateProjectNames(t *testing.T) {
	path := writeYAML(t, `
target: /tmp/backups.sqlite3
backup_self: false
projects:
  - name: app1
    base_dir: /tmp
    file: app1.db
    command: echo {{file}}
  - name: app1
    base_dir: /tmp
    file: app2.db
    command: echo {{file}}
`)

	if _, err := Load(path); err == nil {
		t.Fatal("Load returned nil error, want error for duplicate project names")
	}
}

func TestLoad_BackupSelfRequiresSelfCommand(t *testing.T) {
	path := writeYAML(t, `
target: /tmp/backups.sqlite3
backup_self: true
projects:
  - name: app1
    base_dir: /tmp
    file: app1.db
    command: echo {{file}}
`)

	if _, err := Load(path); err == nil {
		t.Fatal("Load returned nil error, want error for missing self_command")
	}
}

func TestLoad_ExpandsEnvVarsAndTilde(t *testing.T) {
	t.Setenv("BKP_TEST_DIR", "/from/env")

	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("no home dir available: %v", err)
	}

	path := writeYAML(t, `
target: $BKP_TEST_DIR/backups.sqlite3
backup_self: false
projects:
  - name: app1
    base_dir: ~/dbs
    file: app1.db
    command: echo {{file}}
`)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load returned unexpected error: %v", err)
	}

	wantTarget := "/from/env/backups.sqlite3"
	if cfg.Target != wantTarget {
		t.Errorf("Target = %q, want %q", cfg.Target, wantTarget)
	}

	wantBaseDir := filepath.Join(home, "dbs")
	if cfg.Projects[0].BaseDir != wantBaseDir {
		t.Errorf("BaseDir = %q, want %q", cfg.Projects[0].BaseDir, wantBaseDir)
	}
}

func TestLoad_BackupSelfFalseSkipsSelfCommand(t *testing.T) {
	path := writeYAML(t, `
target: /tmp/backups.sqlite3
backup_self: false
projects:
  - name: app1
    base_dir: /tmp
    file: app1.db
    command: echo {{file}}
`)

	if _, err := Load(path); err != nil {
		t.Fatalf("Load returned unexpected error: %v", err)
	}
}
