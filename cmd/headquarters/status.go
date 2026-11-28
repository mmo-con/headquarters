package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/mmo-con/headquarters/internal/app"
	"github.com/mmo-con/headquarters/internal/domain"
)

// exitFailOn is the exit status when a repository matches a --fail-on state.
const exitFailOn = 3

// runStatus implements "headquarters status": it prints the status of the
// repositories without starting the user interface. See ADR 0016.
func runStatus(args []string, stdout, stderr io.Writer, getenv func(string) string) int {
	flags := flag.NewFlagSet("headquarters status", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configFlag := flags.String("config", "", "path of the configuration file")
	format := flags.String("format", "json", "output `format`: json (one document) or ndjson (one repository per line)")
	filter := flags.String("filter", "", "report only repositories matching the dashboard filter `expression`")
	fetch := flags.Bool("fetch", false, "fetch the matching repositories first; without it, no network access")
	failOn := flags.String("fail-on", "", "exit with status 3 if a reported repository is in one of the comma-separated `states`")
	flags.Usage = func() {
		fmt.Fprintln(stderr, "usage: headquarters status [options] [path]")
		fmt.Fprintln(stderr, "\nInspects the repositories and prints their status without starting the")
		fmt.Fprintln(stderr, "user interface. It never changes a repository. With a path, the")
		fmt.Fprintln(stderr, "repositories below it replace the configured ones.")
		fmt.Fprintln(stderr, "\nStates for --fail-on: "+strings.Join(app.StatusStates(), ", "))
		fmt.Fprintln(stderr, "\nExit status: 0 success, 1 runtime error, 2 usage or configuration error,")
		fmt.Fprintln(stderr, "3 a repository matches a --fail-on state.")
		fmt.Fprintln(stderr, "\noptions:")
		flags.PrintDefaults()
	}
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	usageError := func(format string, a ...any) int {
		fmt.Fprintf(stderr, "headquarters status: "+format+"\n", a...)
		return 2
	}
	if *format != "json" && *format != "ndjson" {
		return usageError("unknown format %q; use json or ndjson", *format)
	}
	if err := app.ValidateFilter(*filter, false); err != nil {
		return usageError("invalid filter: %v", err)
	}
	var states []string
	if *failOn != "" {
		for _, s := range strings.Split(*failOn, ",") {
			states = append(states, strings.TrimPrefix(strings.ToLower(strings.TrimSpace(s)), "is:"))
		}
		if err := app.ValidateStatusStates(states); err != nil {
			return usageError("--fail-on: %v", err)
		}
	}
	scanRoot, err := scanRootArg(flags.Args())
	if err != nil {
		return usageError("%v", err)
	}

	a, code := openApp(*configFlag, scanRoot, stderr, getenv)
	if a == nil {
		return code
	}
	defer a.Close()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	st := a.CollectStatus(ctx, app.StatusOptions{Filter: app.Filter{Text: *filter}, Fetch: *fetch})
	for _, w := range st.Warnings {
		fmt.Fprintln(stderr, "headquarters: warning:", domain.Printable(w))
	}
	if ctx.Err() != nil {
		fmt.Fprintln(stderr, "headquarters: interrupted")
		return 1
	}

	report := app.NewReport(&st, time.Now())
	if err := writeReport(stdout, report, *format); err != nil {
		fmt.Fprintln(stderr, "headquarters:", err)
		return 1
	}
	for i := range st.Repositories {
		for _, s := range states {
			if st.Is(&st.Repositories[i], s) {
				return exitFailOn
			}
		}
	}
	return 0
}

func writeReport(w io.Writer, report app.Report, format string) error {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	if format == "json" {
		enc.SetIndent("", "  ")
		return enc.Encode(report)
	}
	for _, r := range report.Repositories {
		line := struct {
			Schema int `json:"schema"`
			app.RepositoryReport
		}{report.Schema, r}
		if err := enc.Encode(line); err != nil {
			return err
		}
	}
	return nil
}
