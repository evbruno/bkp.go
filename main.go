package main

import (
	"flag"
	"fmt"
	"os"
	"text/tabwriter"
	"time"

	"github.com/evbruno/bkp.go/internal/config"
	"github.com/evbruno/bkp.go/internal/runner"
	"github.com/evbruno/bkp.go/internal/store"
)

// version is set at build time via -ldflags "-X main.version=...".
var version = "dev"

func main() {
	if len(os.Args) > 1 && os.Args[1] == "status" {
		runStatus(os.Args[2:])
		return
	}
	runBackup(os.Args[1:])
}

func runBackup(args []string) {
	fs := flag.NewFlagSet("bkp", flag.ExitOnError)
	configPath := fs.String("config", "", "path to backup spec YAML (required)")
	dryRun := fs.Bool("dry-run", false, "validate config and report what would run, without executing anything")
	showVersion := fs.Bool("version", false, "print version and exit")
	fs.Parse(args)

	if *showVersion {
		fmt.Println(version)
		return
	}

	if *configPath == "" {
		fmt.Fprintln(os.Stderr, "error: --config is required")
		fs.Usage()
		os.Exit(2)
	}

	cfg, err := config.Load(*configPath)
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

// runStatus is read-only: it never runs a project's command or writes a
// backup_log row, it only reports the latest logged row per project.
func runStatus(args []string) {
	fs := flag.NewFlagSet("bkp status", flag.ExitOnError)
	configPath := fs.String("config", "", "path to backup spec YAML (required)")
	fs.Parse(args)

	if *configPath == "" {
		fmt.Fprintln(os.Stderr, "error: --config is required")
		fs.Usage()
		os.Exit(2)
	}

	cfg, err := config.Load(*configPath)
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
