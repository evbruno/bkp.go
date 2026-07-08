package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/evbruno/bkp.go/internal/config"
	"github.com/evbruno/bkp.go/internal/runner"
	"github.com/evbruno/bkp.go/internal/store"
)

// version is set at build time via -ldflags "-X main.version=...".
var version = "dev"

func main() {
	configPath := flag.String("config", "", "path to backup spec YAML (required)")
	dryRun := flag.Bool("dry-run", false, "validate config and report what would run, without executing anything")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Println(version)
		return
	}

	if *configPath == "" {
		fmt.Fprintln(os.Stderr, "error: --config is required")
		flag.Usage()
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

func printSummary(title string, results []runner.Result) bool {
	fmt.Printf("Backup summary: %s\n", title)
	fmt.Println(strings.Repeat("-", 70))

	failed := false
	for _, r := range results {
		line := fmt.Sprintf("%-30s %-8s %8s", r.Project, r.Status, r.Duration.Round(time.Millisecond))
		if r.Status == "error" {
			failed = true
			line += fmt.Sprintf("  error=%s", r.Error)
		}
		fmt.Println(line)
	}

	fmt.Println(strings.Repeat("-", 70))
	return failed
}
