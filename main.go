package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/evbruno/bkp.go/internal/config"
	"github.com/evbruno/bkp.go/internal/runner"
	"github.com/evbruno/bkp.go/internal/selfupdate"
	"github.com/evbruno/bkp.go/internal/store"
)

// version/commit/date are set at build time via -ldflags "-X main.*=...".
var version = "dev"
var commit = "unknown"
var date = "unknown"

func main() {
	if len(os.Args) > 1 && os.Args[1] == "version" {
		printVersionReport()
		return
	}

	if len(os.Args) > 1 && os.Args[1] == "status" {
		runStatus(os.Args[2:])
		return
	}

	if len(os.Args) > 1 && os.Args[1] == "update" {
		runUpdate(os.Args[2:])
		return
	}
	runBackup(os.Args[1:])
}

func runBackup(args []string) {
	fs := flag.NewFlagSet("bkp", flag.ExitOnError)
	configPath := fs.String("config", "", "path to backup spec YAML (optional: defaults to BKP_CONFIG or $HOME/.config/bkp/bkp.yaml)")
	dryRun := fs.Bool("dry-run", false, "validate config and report what would run, without executing anything")
	showVersion := fs.Bool("version", false, "print version and exit")
	fs.Usage = func() {
		out := fs.Output()
		fmt.Fprint(out, `Usage: bkp [flags]          run every project's backup
       bkp status [flags]   show the latest logged run per project
       bkp update [-force]  replace bkp on $PATH with the latest release
       bkp version          print version and build details

Flags:
`)
		fs.PrintDefaults()
	}
	parseArgs(fs, args)

	if *showVersion {
		printVersionReport()
		return
	}

	resolvedConfigPath, configSource, err := config.ResolveConfigPath(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("config: %s (from %s)\n", resolvedConfigPath, configSource)

	cfg, err := config.Load(resolvedConfigPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	st, err := store.Open(cfg.Target)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	defer st.Close()

	results := runner.Run(cfg, st, runner.Options{
		DryRun: *dryRun,
		Progress: func(project, msg string) {
			fmt.Printf("%s [%s] %s\n", time.Now().Format("15:04:05"), project, msg)
		},
	})
	fmt.Println()

	if printSummary(cfg.Title, results) {
		os.Exit(1)
	}
}

func printVersionReport() {
	goLinking := "unknown"
	goTags := "none"

	if info, ok := debug.ReadBuildInfo(); ok {
		for _, s := range info.Settings {
			switch s.Key {
			case "CGO_ENABLED":
				switch s.Value {
				case "1":
					goLinking = "dynamic"
				case "0":
					goLinking = "static"
				}
			case "-tags":
				if strings.TrimSpace(s.Value) != "" {
					goTags = s.Value
				}
			}
		}
	}

	rows := [][2]string{
		{"bkp", version},
		{"repo", selfupdate.DefaultRepoURL},
		{"commit", commit},
		{"date", date},
		{"os/version", osVersion()},
		{"os/kernel", fmt.Sprintf("%s (%s)", osKernelVersion(), osKernelArch())},
		{"os/type", runtime.GOOS},
		{"os/arch", runtime.GOARCH},
		{"go/version", runtime.Version()},
		{"go/linking", goLinking},
		{"go/tags", goTags},
	}

	printBorderedKVTable(rows)
}

func printBorderedKVTable(rows [][2]string) {
	if len(rows) == 0 {
		return
	}

	keyWidth := 0
	valueWidth := 0
	for _, row := range rows {
		if len(row[0]) > keyWidth {
			keyWidth = len(row[0])
		}
		if len(row[1]) > valueWidth {
			valueWidth = len(row[1])
		}
	}

	border := "+" + strings.Repeat("-", keyWidth+2) + "+" + strings.Repeat("-", valueWidth+2) + "+"

	fmt.Println(border)
	for _, row := range rows {
		fmt.Printf("| %-*s | %-*s |\n", keyWidth, row[0], valueWidth, row[1])
	}
	fmt.Println(border)
}

func osVersion() string {
	bits := strconv.Itoa(strconv.IntSize)

	version := "unknown"
	switch runtime.GOOS {
	case "darwin":
		if out, err := exec.Command("sw_vers", "-productVersion").Output(); err == nil {
			version = strings.TrimSpace(string(out))
		}
	case "linux":
		if out, err := exec.Command("uname", "-r").Output(); err == nil {
			version = strings.TrimSpace(string(out))
		}
	}

	return fmt.Sprintf("%s %s (%s bit)", runtime.GOOS, version, bits)
}

func osKernelVersion() string {
	if out, err := exec.Command("uname", "-r").Output(); err == nil {
		return strings.TrimSpace(string(out))
	}
	return "unknown"
}

func osKernelArch() string {
	if out, err := exec.Command("uname", "-m").Output(); err == nil {
		return strings.TrimSpace(string(out))
	}
	return runtime.GOARCH
}

// parseArgs parses flags and rejects leftover positional arguments, so a
// mistyped subcommand (e.g. "bkp self-update") fails instead of silently
// falling through to a backup run.
func parseArgs(fs *flag.FlagSet, args []string) {
	fs.Parse(args)
	if fs.NArg() > 0 {
		fmt.Fprintf(os.Stderr, "error: unknown command or argument %q\n", fs.Arg(0))
		fs.Usage()
		os.Exit(2)
	}
}

// runUpdate replaces the bkp found on $PATH with the latest GitHub release
// build for this platform, keeping the previous binary at /tmp/bkp.bkp.
func runUpdate(args []string) {
	fs := flag.NewFlagSet("bkp update", flag.ExitOnError)
	force := fs.Bool("force", false, "reinstall even if already on the latest version")
	parseArgs(fs, args)

	fail := func(err error) {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	target, err := exec.LookPath("bkp")
	if err != nil {
		fail(fmt.Errorf("finding bkp on $PATH: %w", err))
	}

	u := &selfupdate.Updater{RepoURL: selfupdate.DefaultRepoURL, GOOS: runtime.GOOS, GOARCH: runtime.GOARCH}
	tag, err := u.LatestTag()
	if err != nil {
		fail(err)
	}

	if tag == version && !*force {
		fmt.Printf("bkp %s is already the latest version (%s)\n", version, target)
		return
	}

	fmt.Printf("downloading bkp %s (%s)...\n", tag, u.AssetName())
	bin, err := u.Download(tag)
	if err != nil {
		fail(err)
	}

	if err := selfupdate.Replace(target, bin, selfupdate.DefaultBackupPath); err != nil {
		fail(err)
	}
	fmt.Printf("updated %s: %s -> %s (previous binary saved to %s)\n", target, version, tag, selfupdate.DefaultBackupPath)
}

// runStatus is read-only: it never runs a project's command or writes a
// backup_log row, it only reports the latest logged row per project.
func runStatus(args []string) {
	fs := flag.NewFlagSet("bkp status", flag.ExitOnError)
	configPath := fs.String("config", "", "path to backup spec YAML (optional: defaults to BKP_CONFIG or $HOME/.config/bkp/bkp.yaml)")
	parseArgs(fs, args)

	resolvedConfigPath, _, err := config.ResolveConfigPath(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	cfg, err := config.Load(resolvedConfigPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	st, err := store.Open(cfg.Target)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	defer st.Close()

	rows, err := st.LatestPerProject()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	printLatest(rows)
}

func printSummary(title string, results []runner.Result) bool {
	fmt.Printf("Backup summary: %s\n", title)

	w := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
	fmt.Fprintln(w, "PROJECT\tBASE DIR\tFILE\tSENT\tSHA1\tSTATUS\tDURATION\tERROR")

	failed := false
	for _, r := range results {
		if r.Status == "error" {
			failed = true
		}

		sha1 := r.SHA1
		if sha1 == "" {
			sha1 = "-"
		}
		sent := r.Artifact
		if sent == "" {
			sent = "-"
		}

		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			r.Project,
			r.BaseDir,
			r.FileName,
			sent,
			sha1,
			r.Status,
			r.Duration.Round(time.Millisecond),
			r.Error,
		)
	}

	w.Flush()
	return failed
}

func printLatest(rows []store.LogRow) {
	if len(rows) == 0 {
		fmt.Println("No backup_log rows found.")
		return
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
	fmt.Fprintln(w, "PROJECT\tLAST RUN (UTC)\tSTATUS\tSIZE\tGZ SIZE\tDURATION\tERROR")

	for _, r := range rows {
		gzSize := "-"
		if r.CompressedSize != nil {
			gzSize = fmt.Sprintf("%d", *r.CompressedSize)
		}
		duration := time.Duration(r.DurationMs) * time.Millisecond

		fmt.Fprintf(w, "%s\t%s\t%s\t%d\t%s\t%s\t%s\n",
			r.Project,
			r.Timestamp.UTC().Format("2006-01-02 15:04:05"),
			r.Status,
			r.FileSize,
			gzSize,
			duration,
			r.Error,
		)
	}

	w.Flush()
}
