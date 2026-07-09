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
	runBackup(os.Args[1:])
}

func runBackup(args []string) {
	fs := flag.NewFlagSet("bkp", flag.ExitOnError)
	configPath := fs.String("config", "", "path to backup spec YAML (optional: defaults to BKP_CONFIG or $HOME/.config/bkp/bkp.yaml)")
	dryRun := fs.Bool("dry-run", false, "validate config and report what would run, without executing anything")
	showVersion := fs.Bool("version", false, "print version and exit")
	fs.Parse(args)

	if *showVersion {
		printVersionReport()
		return
	}

	resolvedConfigPath, err := config.ResolveConfigPath(*configPath)
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

	results := runner.Run(cfg, st, runner.Options{DryRun: *dryRun})

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

// runStatus is read-only: it never runs a project's command or writes a
// backup_log row, it only reports the latest logged row per project.
func runStatus(args []string) {
	fs := flag.NewFlagSet("bkp status", flag.ExitOnError)
	configPath := fs.String("config", "", "path to backup spec YAML (optional: defaults to BKP_CONFIG or $HOME/.config/bkp/bkp.yaml)")
	fs.Parse(args)

	resolvedConfigPath, err := config.ResolveConfigPath(*configPath)
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
	fmt.Fprintln(w, "PROJECT\tFILE\tSHA1\tSTATUS\tDURATION\tERROR")

	failed := false
	for _, r := range results {
		if r.Status == "error" {
			failed = true
		}

		sha1 := r.SHA1
		if sha1 == "" {
			sha1 = "-"
		}

		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n",
			r.Project,
			r.FileName,
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
