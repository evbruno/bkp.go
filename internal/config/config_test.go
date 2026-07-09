package config

import (
	"os"
	"path/filepath"
	"strings"
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
	if cfg.Projects[0].KeepCompressedEnabled() {
		t.Error("KeepCompressedEnabled() = true, want false (default, since keep_compressed is omitted)")
	}
}

func TestLoad_MissingTarget_UsesDefault(t *testing.T) {
	path := writeYAML(t, `
backup_self: false
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

	want, err := DefaultTargetPath()
	if err != nil {
		t.Fatalf("DefaultTargetPath returned error: %v", err)
	}
	if cfg.Target != want {
		t.Fatalf("Target = %q, want %q", cfg.Target, want)
	}
}

func TestLoad_MissingTarget_UsesBKP_TARGETEnv(t *testing.T) {
	t.Setenv("BKP_TARGET", "$HOME/custom/bkp.sqlite3")

	path := writeYAML(t, `
backup_self: false
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

	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("os.UserHomeDir returned error: %v", err)
	}
	want := filepath.Join(home, "custom", "bkp.sqlite3")
	if cfg.Target != want {
		t.Fatalf("Target = %q, want %q", cfg.Target, want)
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

func TestResolveConfigPath_CLIWins(t *testing.T) {
	t.Setenv("BKP_CONFIG", "/tmp/from-env.yaml")

	got, err := ResolveConfigPath("~/from-cli.yaml")
	if err != nil {
		t.Fatalf("ResolveConfigPath returned error: %v", err)
	}

	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("os.UserHomeDir returned error: %v", err)
	}
	want := filepath.Join(home, "from-cli.yaml")
	if got != want {
		t.Fatalf("ResolveConfigPath = %q, want %q", got, want)
	}
}

func TestResolveConfigPath_EnvWinsWhenCLIMissing(t *testing.T) {
	t.Setenv("BKP_CONFIG", "$HOME/from-env.yaml")

	got, err := ResolveConfigPath("")
	if err != nil {
		t.Fatalf("ResolveConfigPath returned error: %v", err)
	}

	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("os.UserHomeDir returned error: %v", err)
	}
	want := filepath.Join(home, "from-env.yaml")
	if got != want {
		t.Fatalf("ResolveConfigPath = %q, want %q", got, want)
	}
}

func TestResolveConfigPath_DefaultWhenUnset(t *testing.T) {
	t.Setenv("BKP_CONFIG", "")

	got, err := ResolveConfigPath("  ")
	if err != nil {
		t.Fatalf("ResolveConfigPath returned error: %v", err)
	}

	want, err := DefaultConfigPath()
	if err != nil {
		t.Fatalf("DefaultConfigPath returned error: %v", err)
	}
	if got != want {
		t.Fatalf("ResolveConfigPath = %q, want %q", got, want)
	}
}

func TestDefaultPaths(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("os.UserHomeDir returned error: %v", err)
	}

	configPath, err := DefaultConfigPath()
	if err != nil {
		t.Fatalf("DefaultConfigPath returned error: %v", err)
	}
	if !strings.HasPrefix(configPath, home) {
		t.Fatalf("DefaultConfigPath %q does not start with home %q", configPath, home)
	}
	if filepath.Base(configPath) != "bkp.yaml" {
		t.Fatalf("DefaultConfigPath base = %q, want bkp.yaml", filepath.Base(configPath))
	}

	targetPath, err := DefaultTargetPath()
	if err != nil {
		t.Fatalf("DefaultTargetPath returned error: %v", err)
	}
	if !strings.HasPrefix(targetPath, home) {
		t.Fatalf("DefaultTargetPath %q does not start with home %q", targetPath, home)
	}
	if filepath.Base(targetPath) != "bkp.sqlite3" {
		t.Fatalf("DefaultTargetPath base = %q, want bkp.sqlite3", filepath.Base(targetPath))
	}
}
