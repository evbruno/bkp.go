package runner

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/evbruno/bkp.go/internal/config"
	"github.com/evbruno/bkp.go/internal/store"
)

// Result is the outcome of a single project (or the orchestrator self-backup).
type Result struct {
	Project  string
	Status   string // "ok" | "error" | "dry-run"
	Error    string
	Duration time.Duration
}

// Options controls a single Run invocation.
type Options struct {
	DryRun bool
}

// Run executes every project sequentially, then the self-backup if enabled.
// A failing project does not stop the run; the returned slice always has one
// entry per project (plus one for the orchestrator, if backup_self is set).
func Run(cfg *config.Config, st *store.Store, opts Options) []Result {
	results := make([]Result, 0, len(cfg.Projects)+1)

	for _, p := range cfg.Projects {
		results = append(results, runProject(p, st, opts))
	}

	if cfg.BackupSelfEnabled() {
		results = append(results, runSelf(cfg, st, opts))
	}

	return results
}

func runProject(p config.Project, st *store.Store, opts Options) Result {
	start := time.Now()
	sourcePath := filepath.Join(p.BaseDir, p.File)

	fail := func(err error, fileSize int64, compressedSize *int64) Result {
		duration := time.Since(start)
		_ = st.InsertLog(store.LogRow{
			Timestamp:      start,
			Project:        p.Name,
			FilePath:       sourcePath,
			FileSize:       fileSize,
			CompressedSize: compressedSize,
			Status:         "error",
			Error:          err.Error(),
			DurationMs:     duration.Milliseconds(),
		})
		return Result{Project: p.Name, Status: "error", Error: err.Error(), Duration: duration}
	}

	info, err := os.Stat(sourcePath)
	if err != nil {
		return fail(fmt.Errorf("stat source file: %w", err), 0, nil)
	}
	fileSize := info.Size()

	if opts.DryRun {
		return Result{Project: p.Name, Status: "dry-run", Duration: time.Since(start)}
	}

	artifact := p.File
	var compressedSize *int64

	if p.CompressEnabled() {
		gzName := p.File + ".gz"
		if p.TimestampEnabled() {
			gzName = fmt.Sprintf("%s.%s.gz", p.File, isoTimestamp(start))
		}
		gzPath := filepath.Join(p.BaseDir, gzName)

		size, err := gzipFile(sourcePath, gzPath)
		if err != nil {
			return fail(fmt.Errorf("compress: %w", err), fileSize, nil)
		}
		compressedSize = &size
		artifact = gzName
	}

	cmd := substitute(p.Command, artifact)
	if err := runShell(cmd, p.BaseDir); err != nil {
		return fail(fmt.Errorf("command failed: %w", err), fileSize, compressedSize)
	}

	duration := time.Since(start)
	if err := st.InsertLog(store.LogRow{
		Timestamp:      start,
		Project:        p.Name,
		FilePath:       sourcePath,
		FileSize:       fileSize,
		CompressedSize: compressedSize,
		Status:         "ok",
		DurationMs:     duration.Milliseconds(),
	}); err != nil {
		return Result{Project: p.Name, Status: "error", Error: err.Error(), Duration: duration}
	}

	return Result{Project: p.Name, Status: "ok", Duration: duration}
}

func runSelf(cfg *config.Config, st *store.Store, opts Options) Result {
	const project = "orchestrator"
	start := time.Now()

	if opts.DryRun {
		return Result{Project: project, Status: "dry-run", Duration: time.Since(start)}
	}

	var fileSize int64
	if info, err := os.Stat(cfg.Target); err == nil {
		fileSize = info.Size()
	}

	// Logged before self_command runs, per PLAN.md: the copied DB is
	// consistent-ish since the row for this very run is already present.
	if err := st.InsertLog(store.LogRow{
		Timestamp: start,
		Project:   project,
		FilePath:  cfg.Target,
		FileSize:  fileSize,
		Status:    "ok",
	}); err != nil {
		return Result{Project: project, Status: "error", Error: err.Error(), Duration: time.Since(start)}
	}

	targetDir := filepath.Dir(cfg.Target)
	if err := runShell(cfg.SelfCommand, targetDir); err != nil {
		return Result{Project: project, Status: "error", Error: err.Error(), Duration: time.Since(start)}
	}

	return Result{Project: project, Status: "ok", Duration: time.Since(start)}
}

// isoTimestamp formats t as basic-format ISO 8601 UTC (e.g. 20260708T195149Z)
// suitable for filenames: no colons or other separators shells/filesystems
// treat specially, while still being unambiguously ISO 8601.
func isoTimestamp(t time.Time) string {
	return t.UTC().Format("20060102T150405Z")
}

func substitute(command, artifact string) string {
	if strings.Contains(command, "{{file}}") {
		return strings.ReplaceAll(command, "{{file}}", artifact)
	}
	return command
}

func runShell(command, dir string) error {
	cmd := exec.Command("sh", "-c", command)
	cmd.Dir = dir

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return fmt.Errorf("%w: %s", err, msg)
		}
		return err
	}
	return nil
}

func gzipFile(src, dst string) (int64, error) {
	in, err := os.Open(src)
	if err != nil {
		return 0, err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return 0, err
	}
	defer out.Close()

	gw := gzip.NewWriter(out)
	if _, err := io.Copy(gw, in); err != nil {
		gw.Close()
		return 0, err
	}
	if err := gw.Close(); err != nil {
		return 0, err
	}

	info, err := out.Stat()
	if err != nil {
		return 0, err
	}
	return info.Size(), nil
}
