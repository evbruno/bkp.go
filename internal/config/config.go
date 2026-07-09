package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	defaultConfigFile = "bkp.yaml"
	defaultTargetFile = "bkp.sqlite3"
)

// Project describes a single backup unit: a source file under base_dir,
// compressed (by default) and handed to command as {{file}}.
type Project struct {
	Name           string `yaml:"name"`
	BaseDir        string `yaml:"base_dir"`
	File           string `yaml:"file"`
	Command        string `yaml:"command"`
	Compress       *bool  `yaml:"compress"`
	Timestamp      *bool  `yaml:"timestamp"`
	SkipUnchanged  *bool  `yaml:"skip_unchanged"`
	KeepCompressed *bool  `yaml:"keep_compressed"`
}

// CompressEnabled returns the effective compress setting, defaulting to true.
func (p *Project) CompressEnabled() bool {
	if p.Compress == nil {
		return true
	}
	return *p.Compress
}

// TimestampEnabled returns the effective timestamp setting, defaulting to
// true. Only relevant when CompressEnabled is true: it controls whether the
// gzip artifact's filename gets a timestamp suffix, so successive runs don't
// overwrite each other's compressed output.
func (p *Project) TimestampEnabled() bool {
	if p.Timestamp == nil {
		return true
	}
	return *p.Timestamp
}

// SkipUnchangedEnabled returns the effective skip_unchanged setting,
// defaulting to true: if the source file's sha1 matches the sha1 of the
// project's last successful backup, the run is skipped entirely (no
// compression, no command execution).
func (p *Project) SkipUnchangedEnabled() bool {
	if p.SkipUnchanged == nil {
		return true
	}
	return *p.SkipUnchanged
}

// KeepCompressedEnabled returns the effective keep_compressed setting,
// defaulting to false: once command succeeds, the gzip artifact produced by
// CompressEnabled is deleted. Set keep_compressed: true to leave it on disk.
// Only relevant when CompressEnabled is true.
func (p *Project) KeepCompressedEnabled() bool {
	if p.KeepCompressed == nil {
		return false
	}
	return *p.KeepCompressed
}

// Config is the top-level backup spec loaded from YAML.
type Config struct {
	Title       string    `yaml:"title"`
	Target      string    `yaml:"target"`
	BackupSelf  *bool     `yaml:"backup_self"`
	SelfCommand string    `yaml:"self_command"`
	Projects    []Project `yaml:"projects"`
}

// ResolveConfigPath returns the config path by precedence:
// CLI flag > BKP_CONFIG env var > default ($HOME/.config/bkp/bkp.yaml).
func ResolveConfigPath(cliPath string) (string, error) {
	if strings.TrimSpace(cliPath) != "" {
		return expandPath(cliPath), nil
	}

	if p, ok := os.LookupEnv("BKP_CONFIG"); ok && strings.TrimSpace(p) != "" {
		return expandPath(p), nil
	}

	return DefaultConfigPath()
}

// DefaultConfigPath returns the default config location.
func DefaultConfigPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolving home directory for default config path: %w", err)
	}
	return filepath.Join(home, ".config", "bkp", defaultConfigFile), nil
}

// DefaultTargetPath returns the default orchestrator SQLite path.
func DefaultTargetPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolving home directory for default target path: %w", err)
	}
	return filepath.Join(home, ".local", "state", "bkp", defaultTargetFile), nil
}

// BackupSelfEnabled returns the effective backup_self setting, defaulting to true.
func (c *Config) BackupSelfEnabled() bool {
	if c.BackupSelf == nil {
		return true
	}
	return *c.BackupSelf
}

// Load reads, parses, and validates a backup spec from path.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config: %w", err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}

	if err := cfg.applyDefaults(); err != nil {
		return nil, err
	}

	cfg.expandPaths()

	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	return &cfg, nil
}

func (c *Config) applyDefaults() error {
	if strings.TrimSpace(c.Target) == "" {
		if envTarget, ok := os.LookupEnv("BKP_TARGET"); ok && strings.TrimSpace(envTarget) != "" {
			c.Target = envTarget
			return nil
		}

		p, err := DefaultTargetPath()
		if err != nil {
			return err
		}
		c.Target = p
	}

	return nil
}

// expandPaths expands $VAR / ${VAR} and a leading ~ in fields that are used
// directly as filesystem paths (i.e. not passed through a shell, which would
// otherwise expand them itself). command / self_command run via "sh -c" and
// are left untouched: the shell expands those on its own.
func (c *Config) expandPaths() {
	c.Target = expandPath(c.Target)
	for i := range c.Projects {
		c.Projects[i].BaseDir = expandPath(c.Projects[i].BaseDir)
		c.Projects[i].File = expandPath(c.Projects[i].File)
	}
}

func expandPath(s string) string {
	s = os.ExpandEnv(s)

	if s == "~" || strings.HasPrefix(s, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			s = filepath.Join(home, strings.TrimPrefix(s, "~"))
		}
	}

	return s
}

// Validate applies the rules documented in PLAN.md.
func (c *Config) Validate() error {
	if strings.TrimSpace(c.Target) == "" {
		return fmt.Errorf("target is required")
	}
	if len(c.Projects) == 0 {
		return fmt.Errorf("projects: at least one entry is required")
	}

	seen := make(map[string]bool, len(c.Projects))
	for i, p := range c.Projects {
		if p.Name == "" {
			return fmt.Errorf("projects[%d]: name is required", i)
		}
		if p.BaseDir == "" {
			return fmt.Errorf("project %q: base_dir is required", p.Name)
		}
		if p.File == "" {
			return fmt.Errorf("project %q: file is required", p.Name)
		}
		if p.Command == "" {
			return fmt.Errorf("project %q: command is required", p.Name)
		}
		if seen[p.Name] {
			return fmt.Errorf("duplicate project name: %q", p.Name)
		}
		seen[p.Name] = true
	}

	if c.BackupSelfEnabled() && c.SelfCommand == "" {
		return fmt.Errorf("self_command is required when backup_self is true")
	}

	return nil
}
