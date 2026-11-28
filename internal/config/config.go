// Package config loads and validates the headquarters configuration file.
package config

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Config is the validated configuration with all paths expanded.
type Config struct {
	Version      int
	File         string // absolute path of the loaded file, empty if none exists
	Repositories []Repository
	Discovery    []Discovery
	Git          Git
	Branches     Branches
	Lazygit      Lazygit
	Launchers    []Launcher
	UI           UI
	Warnings     []string
}

type Repository struct {
	Path       string
	Name       string // empty means derived from the path
	Family     string
	Workspace  string // empty, absolute, or relative to the repository
	Enabled    bool
	Remote     string
	RemoteName string
	Untracked  string // empty means the [git] default
	// PinnedBranches replaces [branches].pinned if not nil; configured but
	// missing branches are then reported.
	PinnedBranches []string
}

type Discovery struct {
	Root           string
	MaxDepth       int
	Family         string
	Workspace      string // empty, absolute, or relative to the repository
	Include        []string
	Exclude        []string
	FollowSymlinks bool
}

// ScanOnly replaces the configured inventory with discovery below root, using
// the discovery defaults. All other settings are kept.
func (c *Config) ScanOnly(root string) {
	c.Repositories = nil
	c.Discovery = []Discovery{{Root: root, MaxDepth: DefaultMaxDepth, Include: []string{"**"}}}
}

type Git struct {
	Executable            string
	InspectionParallelism int
	InspectionTimeout     time.Duration
	FetchParallelism      int
	FetchTimeout          time.Duration
	FetchPrune            bool
	Untracked             string
}

// Branches configures the reporting of local branches other than HEAD.
type Branches struct {
	Enabled bool
	Pinned  []string
	Ignore  []string
	MaxAge  time.Duration // 0 disables the age filter
}

type Lazygit struct {
	Command string
	Args    []string
	Enabled bool
}

// UI configures the appearance of the user interface.
type UI struct {
	Icons string // "unicode", "nerd", or "ascii"
	View  string // initial repository view: "list" or "tree"
	Sort  string // initial sort order, one of SortOrders
	// DetailCommits is the number of recent commits in the detail panel.
	DetailCommits int
	// IncomingCommits is the number of incoming upstream commits listed in
	// the detail panel, the update confirmation, and the operations view.
	IncomingCommits int
	// Theme is the theme for dark backgrounds, and for light ones unless
	// LightTheme is set.
	Theme Theme
	// LightTheme is the theme for light backgrounds if the preset is a
	// theme family; nil otherwise.
	LightTheme *Theme
}

// ThemeFor returns the theme for a dark or light terminal background.
func (u UI) ThemeFor(dark bool) Theme {
	if !dark && u.LightTheme != nil {
		return *u.LightTheme
	}
	return u.Theme
}

// SortOrders names the sort orders of [ui].sort, in the order of the sort
// overlay.
var SortOrders = []string{"name", "family", "commit", "worktree", "upstream", "health"}

// Theme holds colors: ANSI names, numbers 0-255, or #rrggbb.
type Theme struct {
	ActiveBorder       string
	InactiveBorder     string
	Title              string
	SelectedBackground string
	SelectedForeground string
	SelectedAccent     string
	HeaderBackground   string
	HeaderForeground   string
	Key                string
	Branch             string
	Clean              string
	Dirty              string
	Ahead              string
	Behind             string
	Success            string
	Error              string
	Warning            string
	Dim                string
}

type Launcher struct {
	Name    string
	Command []string
}

// Duration decodes Go duration strings such as "10s".
type Duration struct{ time.Duration }

func (d *Duration) UnmarshalText(b []byte) error {
	v, err := time.ParseDuration(string(b))
	if err != nil {
		return fmt.Errorf("invalid duration %q: %w", string(b), err)
	}
	if v <= 0 {
		return fmt.Errorf("duration %q must be positive", string(b))
	}
	d.Duration = v
	return nil
}

// Age decodes an age such as "30d", a Go duration such as "12h", or "0".
type Age struct{ time.Duration }

func (a *Age) UnmarshalText(b []byte) error {
	s := string(b)
	if s == "0" {
		a.Duration = 0
		return nil
	}
	if days, ok := strings.CutSuffix(s, "d"); ok {
		n, err := strconv.Atoi(days)
		if err != nil || n < 0 {
			return fmt.Errorf("invalid age %q; use for example \"30d\" or \"0\"", s)
		}
		a.Duration = time.Duration(n) * 24 * time.Hour
		return nil
	}
	v, err := time.ParseDuration(s)
	if err != nil || v < 0 {
		return fmt.Errorf("invalid age %q; use for example \"30d\" or \"0\"", s)
	}
	a.Duration = v
	return nil
}

// file mirrors the TOML layout. Pointers distinguish absent values.
type file struct {
	Version      int              `toml:"version"`
	Repositories []fileRepository `toml:"repositories"`
	Discovery    []fileDiscovery  `toml:"discovery"`
	Git          fileGit          `toml:"git"`
	Branches     fileBranches     `toml:"branches"`
	Lazygit      fileLazygit      `toml:"lazygit"`
	Launchers    []fileLauncher   `toml:"launchers"`
	UI           fileUI           `toml:"ui"`
}

type fileUI struct {
	Icons           string    `toml:"icons"`
	View            string    `toml:"view"`
	Sort            string    `toml:"sort"`
	DetailCommits   *int      `toml:"detail_commits"`
	IncomingCommits *int      `toml:"incoming_commits"`
	Theme           fileTheme `toml:"theme"`
}

type fileTheme struct {
	Preset             string `toml:"preset"`
	ActiveBorder       string `toml:"active_border"`
	InactiveBorder     string `toml:"inactive_border"`
	Title              string `toml:"title"`
	SelectedBackground string `toml:"selected_background"`
	SelectedForeground string `toml:"selected_foreground"`
	SelectedAccent     string `toml:"selected_accent"`
	HeaderBackground   string `toml:"header_background"`
	HeaderForeground   string `toml:"header_foreground"`
	Key                string `toml:"key"`
	Branch             string `toml:"branch"`
	Clean              string `toml:"clean"`
	Dirty              string `toml:"dirty"`
	Ahead              string `toml:"ahead"`
	Behind             string `toml:"behind"`
	Success            string `toml:"success"`
	Error              string `toml:"error"`
	Warning            string `toml:"warning"`
	Dim                string `toml:"dim"`
}

type fileRepository struct {
	Path       string `toml:"path"`
	Name       string `toml:"name"`
	Family     string `toml:"family"`
	Workspace  string `toml:"workspace"`
	Enabled    *bool  `toml:"enabled"`
	Remote     string `toml:"remote"`
	RemoteName string `toml:"remote_name"`
	Untracked  string `toml:"untracked"`
	// A pointer distinguishes an absent list from an empty one.
	PinnedBranches *[]string `toml:"pinned_branches"`
}

type fileBranches struct {
	Enabled *bool     `toml:"enabled"`
	Pinned  *[]string `toml:"pinned"`
	Ignore  []string  `toml:"ignore"`
	MaxAge  *Age      `toml:"max_age"`
}

type fileDiscovery struct {
	Root           string   `toml:"root"`
	MaxDepth       *int     `toml:"max_depth"`
	Family         string   `toml:"family"`
	Workspace      string   `toml:"workspace"`
	Include        []string `toml:"include"`
	Exclude        []string `toml:"exclude"`
	FollowSymlinks bool     `toml:"follow_symlinks"`
}

type fileGit struct {
	Executable            string    `toml:"executable"`
	InspectionParallelism *int      `toml:"inspection_parallelism"`
	InspectionTimeout     *Duration `toml:"inspection_timeout"`
	FetchParallelism      *int      `toml:"fetch_parallelism"`
	FetchTimeout          *Duration `toml:"fetch_timeout"`
	FetchPrune            *bool     `toml:"fetch_prune"`
	Untracked             string    `toml:"untracked"`
}

type fileLazygit struct {
	Command string   `toml:"command"`
	Args    []string `toml:"args"`
	Enabled *bool    `toml:"enabled"`
}

type fileLauncher struct {
	Name    string   `toml:"name"`
	Command []string `toml:"command"`
}
