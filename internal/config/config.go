package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Project describes a single backup unit: a source file under base_dir,
// compressed (by default) and handed to command as {{file}}.
type Project struct {
	Name      string `yaml:"name"`
	BaseDir   string `yaml:"base_dir"`
	File      string `yaml:"file"`
	Command   string `yaml:"command"`
	Compress  *bool  `yaml:"compress"`
	Timestamp *bool  `yaml:"timestamp"`
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

// Config is the top-level backup spec loaded from YAML.
type Config struct {
	Title       string    `yaml:"title"`
	Target      string    `yaml:"target"`
	BackupSelf  *bool     `yaml:"backup_self"`
	SelfCommand string    `yaml:"self_command"`
	Projects    []Project `yaml:"projects"`
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

	cfg.expandPaths()

	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	return &cfg, nil
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
	if c.Target == "" {
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
