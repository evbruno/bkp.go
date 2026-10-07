package runner

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha1"
	"encoding/hex"
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
	FileName string // base name of the source file, e.g. "production1.sqlite3"
	SHA1     string // sha1 of the uncompressed source file; empty if never computed (dry-run, stat failure, orchestrator)
	Status   string // "ok" | "error" | "dry-run" | "skipped"
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

	fail := func(err error, fileSize int64, compressedSize *int64, sha1sum string) Result {
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
			SHA1:           sha1sum,
		})
		return Result{Project: p.Name, FileName: p.File, SHA1: sha1sum, Status: "error", Error: err.Error(), Duration: duration}
	}

	info, err := os.Stat(sourcePath)
	if err != nil {
		return fail(fmt.Errorf("stat source file: %w", err), 0, nil, "")
	}
	isDir := info.IsDir()
	fileSize := info.Size()
	if isDir {
		if fileSize, err = dirSize(sourcePath); err != nil {
			return fail(fmt.Errorf("stat source dir: %w", err), 0, nil, "")
		}
	}

	if opts.DryRun {
		return Result{Project: p.Name, FileName: p.File, Status: "dry-run", Duration: time.Since(start)}
	}

	var sha1sum string
	if isDir {
		sha1sum, err = sha1Dir(sourcePath)
	} else {
		sha1sum, err = sha1File(sourcePath)
	}
	if err != nil {
		return fail(fmt.Errorf("hash source file: %w", err), fileSize, nil, "")
	}

	if p.SkipUnchangedEnabled() {
		lastSHA1, found, err := st.LatestOKSHA1(p.Name)
		if err != nil {
			return fail(fmt.Errorf("checking last backup: %w", err), fileSize, nil, sha1sum)
		}
		if found && lastSHA1 == sha1sum {
			duration := time.Since(start)
			if err := st.InsertLog(store.LogRow{
				Timestamp:  start,
				Project:    p.Name,
				FilePath:   sourcePath,
				FileSize:   fileSize,
				Status:     "skipped",
				DurationMs: duration.Milliseconds(),
				SHA1:       sha1sum,
			}); err != nil {
				return Result{Project: p.Name, FileName: p.File, SHA1: sha1sum, Status: "error", Error: err.Error(), Duration: duration}
			}
			return Result{Project: p.Name, FileName: p.File, SHA1: sha1sum, Status: "skipped", Duration: duration}
		}
	}

	artifact := p.File
	var compressedSize *int64
	var gzPath string

	if p.CompressEnabled() {
		// A directory is archived as <dir>.tar.gz, next to it in base_dir.
		stem, ext := p.File, ".gz"
		if isDir {
			stem, ext = filepath.Base(sourcePath), ".tar.gz"
		}
		gzName := stem + ext
		if p.TimestampEnabled() {
			gzName = fmt.Sprintf("%s.%s%s", stem, isoTimestamp(start), ext)
		}
		gzPath = filepath.Join(p.BaseDir, gzName)

		var size int64
		if isDir {
			size, err = tarGzDir(sourcePath, gzPath)
		} else {
			size, err = gzipFile(sourcePath, gzPath)
		}
		if err != nil {
			return fail(fmt.Errorf("compress: %w", err), fileSize, nil, sha1sum)
		}
		compressedSize = &size
		artifact = gzName
	}

	cmd := substitute(p.Command, artifact)
	if err := runShell(cmd, p.BaseDir); err != nil {
		return fail(fmt.Errorf("command failed: %w", err), fileSize, compressedSize, sha1sum)
	}

	if p.CompressEnabled() && !p.KeepCompressedEnabled() {
		if err := os.Remove(gzPath); err != nil {
			return fail(fmt.Errorf("removing compressed artifact: %w", err), fileSize, compressedSize, sha1sum)
		}
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
		SHA1:           sha1sum,
	}); err != nil {
		return Result{Project: p.Name, FileName: p.File, SHA1: sha1sum, Status: "error", Error: err.Error(), Duration: duration}
	}

	return Result{Project: p.Name, FileName: p.File, SHA1: sha1sum, Status: "ok", Duration: duration}
}

func runSelf(cfg *config.Config, st *store.Store, opts Options) Result {
	const project = "orchestrator"
	start := time.Now()
	fileName := filepath.Base(cfg.Target)

	if opts.DryRun {
		return Result{Project: project, FileName: fileName, Status: "dry-run", Duration: time.Since(start)}
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
		return Result{Project: project, FileName: fileName, Status: "error", Error: err.Error(), Duration: time.Since(start)}
	}

	targetDir := filepath.Dir(cfg.Target)
	if err := runShell(cfg.SelfCommand, targetDir); err != nil {
		return Result{Project: project, FileName: fileName, Status: "error", Error: err.Error(), Duration: time.Since(start)}
	}

	return Result{Project: project, FileName: fileName, Status: "ok", Duration: time.Since(start)}
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

// sha1File returns the hex-encoded sha1 of the file at path's uncompressed
// contents, used to detect an unchanged source file across runs.
func sha1File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := sha1.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}

	return hex.EncodeToString(h.Sum(nil)), nil
}

// dirSize returns the total size of the regular files under dir.
func dirSize(dir string) (int64, error) {
	var total int64
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type().IsRegular() {
			info, err := d.Info()
			if err != nil {
				return err
			}
			total += info.Size()
		}
		return nil
	})
	return total, err
}

// sha1Dir returns a hex-encoded sha1 over dir's tree: every entry's relative
// path, plus file contents (or symlink targets), in lexical walk order. Any
// added, removed, renamed or modified entry changes the result.
func sha1Dir(dir string) (string, error) {
	h := sha1.New()
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		fmt.Fprintf(h, "%s\x00%s\x00", d.Type().String(), filepath.ToSlash(rel))

		switch {
		case d.Type()&os.ModeSymlink != 0:
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			fmt.Fprintf(h, "%s\x00", target)
		case d.Type().IsRegular():
			f, err := os.Open(path)
			if err != nil {
				return err
			}
			defer f.Close()
			if _, err := io.Copy(h, f); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// tarGzDir writes dir as a gzip'd tarball to dst, with entries rooted at
// dir's base name (so extracting it recreates the directory), and returns
// the size of dst. Only directories, regular files and symlinks are stored.
func tarGzDir(dir, dst string) (int64, error) {
	out, err := os.Create(dst)
	if err != nil {
		return 0, err
	}
	defer out.Close()

	gw := gzip.NewWriter(out)
	tw := tar.NewWriter(gw)
	root := filepath.Base(dir)

	err = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == dst {
			return nil
		}
		mode := d.Type()
		if !mode.IsDir() && !mode.IsRegular() && mode&os.ModeSymlink == 0 {
			return nil
		}

		info, err := d.Info()
		if err != nil {
			return err
		}
		var link string
		if mode&os.ModeSymlink != 0 {
			if link, err = os.Readlink(path); err != nil {
				return err
			}
		}
		hdr, err := tar.FileInfoHeader(info, link)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		hdr.Name = filepath.ToSlash(filepath.Join(root, rel))
		if mode.IsDir() {
			hdr.Name += "/"
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}

		if mode.IsRegular() {
			f, err := os.Open(path)
			if err != nil {
				return err
			}
			defer f.Close()
			if _, err := io.Copy(tw, f); err != nil {
				return err
			}
		}
		return nil
	})
	if err == nil {
		err = tw.Close()
	}
	if cerr := gw.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return 0, err
	}

	info, err := out.Stat()
	if err != nil {
		return 0, err
	}
	return info.Size(), nil
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
