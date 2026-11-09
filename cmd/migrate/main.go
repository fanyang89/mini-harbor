package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/fanyang89/mini-harbor/internal/legacyimport"
	"github.com/fanyang89/mini-harbor/internal/organization"
)

func main() { os.Exit(run(context.Background(), os.Args[1:], os.Stdout, os.Stderr)) }

func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("mini-harbor-migrate", flag.ContinueOnError)
	flags.SetOutput(stderr)
	path := flags.String("source", "", "Legacy SQLite database (read-only)")
	defaultURLs := os.Getenv("RQLITE_URLS")
	if defaultURLs == "" {
		defaultURLs = "http://localhost:4001"
	}
	urls := flags.String("rqlite-urls", defaultURLs, "Comma-separated nodes in one rqlite cluster")
	dryRun := flags.Bool("dry-run", false, "Compare without writing source or target")
	timeout := flags.Duration("timeout", 2*time.Minute, "Overall migration timeout")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if *path == "" || *timeout <= 0 || flags.NArg() != 0 {
		fmt.Fprintln(stderr, "--source is required, --timeout must be positive, and positional arguments are not accepted")
		return 2
	}
	ctx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()
	source, err := legacyimport.Read(ctx, *path)
	if err != nil {
		fmt.Fprintln(stderr, "Read legacy SQLite:", err)
		return 1
	}
	report, err := organization.ImportOrganizations(ctx, source, strings.Split(*urls, ","), *dryRun)
	if outputErr := json.NewEncoder(stdout).Encode(report); outputErr != nil {
		fmt.Fprintln(stderr, outputErr)
		return 1
	}
	if err != nil {
		fmt.Fprintln(stderr, "Migration failed:", err)
		if errors.Is(err, organization.ErrOutcomeUnknown) {
			fmt.Fprintln(stderr, "Write was not retried. Run --dry-run to reconcile before retrying.")
		}
		return 1
	}
	return 0
}
