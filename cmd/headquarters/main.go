// Command headquarters is a terminal dashboard for a curated collection of
// local Git repositories.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/mmo-con/headquarters/internal/app"
	"github.com/mmo-con/headquarters/internal/config"
	"github.com/mmo-con/headquarters/internal/domain"
	"github.com/mmo-con/headquarters/internal/git"
	"github.com/mmo-con/headquarters/internal/tui"
)

var version = "dev"

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, os.Getenv))
}

// run executes the command line and returns the exit status: 0 on success,
// 1 on runtime errors, and 2 on usage or configuration errors.
func run(args []string, stdout, stderr io.Writer, getenv func(string) string) int {
	if len(args) > 0 {
		switch args[0] {
		case "init":
			return runInit(args[1:], stdout, stderr, getenv)
		case "status":
			return runStatus(args[1:], stdout, stderr, getenv)
		}
	}
	flags := flag.NewFlagSet("headquarters", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configFlag := flags.String("config", "", "path of the configuration file")
	showVersion := flags.Bool("version", false, "print the version and exit")
	checkConfig := flags.Bool("check", false, "validate the configuration, print the inventory, and exit")
	flags.Usage = func() {
		fmt.Fprintln(stderr, "usage: headquarters [options] [path]")
		fmt.Fprintln(stderr, "       headquarters status [options] [path]")
		fmt.Fprintln(stderr, "       headquarters init [options] directory...")
		fmt.Fprintln(stderr, "\nWith a path, the repositories below it replace the configured ones.")
		fmt.Fprintln(stderr, "status prints the repository status as JSON; see headquarters status -h.")
		fmt.Fprintln(stderr, "init prints a starting configuration; see headquarters init -h.")
		fmt.Fprintln(stderr, "\noptions:")
		flags.PrintDefaults()
	}
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}

	if *showVersion {
		fmt.Fprintln(stdout, "headquarters", version)
		return 0
	}

	if a := flags.Arg(0); a == "init" || a == "status" {
		fmt.Fprintf(stderr, "headquarters: %s must be the first argument; use ./%s for a directory named %s\n", a, a, a)
		return 2
	}
	scanRoot, err := scanRootArg(flags.Args())
	if err != nil {
		fmt.Fprintln(stderr, "headquarters:", err)
		return 2
	}

	a, code := openApp(*configFlag, scanRoot, stderr, getenv)
	if a == nil {
		return code
	}
	defer a.Close()

	if *checkConfig {
		return check(a, stdout)
	}

	model := tui.New(a)
	if getenv("NO_COLOR") != "" {
		lipgloss.SetColorProfile(termenv.Ascii)
	} else {
		// Lip Gloss queries the terminal once, before Bubble Tea reads the
		// input, and caches the result, so a theme family chosen later
		// with R uses it too; a failed query counts as dark.
		model.SetDarkBackground(lipgloss.HasDarkBackground())
	}
	if _, err := tea.NewProgram(model, tea.WithAltScreen()).Run(); err != nil {
		fmt.Fprintln(stderr, "headquarters:", err)
		return 1
	}
	if err := model.Err(); err != nil {
		fmt.Fprintln(stderr, "headquarters:", err)
		return 1
	}
	return 0
}

// openApp loads the configuration and opens Git. On failure it reports the
// error and returns nil with the exit status.
func openApp(configFlag, scanRoot string, stderr io.Writer, getenv func(string) string) (*app.Application, int) {
	path, err := config.Locate(configFlag, getenv)
	if err != nil {
		fmt.Fprintln(stderr, "headquarters:", err)
		return nil, 2
	}
	cfg, err := config.Load(path, getenv)
	if err != nil {
		fmt.Fprintln(stderr, "headquarters: invalid configuration:", err)
		return nil, 2
	}
	client, _, err := git.Open(context.Background(), cfg.Git.Executable)
	if err != nil {
		fmt.Fprintln(stderr, "headquarters:", err)
		return nil, 1
	}
	a := app.New(path, cfg, client)
	if scanRoot != "" {
		a.SetScanRoot(scanRoot)
	}
	return a, 0
}

// check prints the resolved inventory without starting the TUI.
func check(a *app.Application, out io.Writer) int {
	if a.Config.File == "" {
		fmt.Fprintf(out, "no configuration file at %s\n", a.ConfigPath)
	} else {
		fmt.Fprintf(out, "configuration: %s\n", a.Config.File)
	}
	if a.ScanRoot != "" {
		fmt.Fprintf(out, "scan of %s (replaces the configured repositories)\n", domain.Printable(a.ScanRoot))
	}
	res := a.LoadInventory(context.Background())
	for _, w := range res.Warnings {
		fmt.Fprintln(out, "warning:", domain.Printable(w))
	}
	for _, r := range res.Repositories {
		line := fmt.Sprintf("%-12s %-10s %-28s %s", domain.Printable(string(r.Family)), r.Source, r.Availability, domain.Printable(r.Path))
		if r.EntryError != nil {
			line += " (" + domain.Printable(r.EntryError.Error()) + ")"
		}
		fmt.Fprintln(out, line)
	}
	if !a.Lazygit.Available() {
		fmt.Fprintln(out, "warning:", a.Lazygit.Err)
	}
	for _, l := range a.Launchers {
		if l.Err != nil {
			fmt.Fprintf(out, "warning: launcher %s: %v\n", l.Name, l.Err)
		}
	}
	fmt.Fprintf(out, "%d repositories\n", len(res.Repositories))
	return 0
}

// scanRootArg validates the optional path argument and returns it as an
// absolute directory, or "" if none was given.
func scanRootArg(args []string) (string, error) {
	switch {
	case len(args) == 0:
		return "", nil
	case len(args) > 1 && strings.HasPrefix(args[1], "-"):
		return "", fmt.Errorf("options must precede the path: %q", args[1])
	case len(args) > 1:
		return "", fmt.Errorf("expected at most one path, got %d", len(args))
	}
	return directoryArg(args[0])
}

// directoryArg returns arg as an absolute path of an existing directory.
func directoryArg(arg string) (string, error) {
	root, err := filepath.Abs(arg)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(root)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%s is not a directory", root)
	}
	return root, nil
}
