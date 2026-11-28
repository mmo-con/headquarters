package config

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

// EnvConfig names the environment variable that selects the configuration file.
const EnvConfig = "HEADQUARTERS_CONFIG"

// Locate returns the configuration path: the explicit flag value, then
// HEADQUARTERS_CONFIG, then the XDG configuration directory.
func Locate(flagValue string, getenv func(string) string) (string, error) {
	if flagValue != "" {
		return expandPath(flagValue, "", getenv)
	}
	if v := getenv(EnvConfig); v != "" {
		return expandPath(v, "", getenv)
	}
	base := getenv("XDG_CONFIG_HOME")
	if base == "" {
		home := getenv("HOME")
		if home == "" {
			return "", errors.New("cannot determine the configuration directory: HOME is not set")
		}
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "headquarters", "config.toml"), nil
}

// Load reads the configuration at path. A missing file yields the default
// configuration with File left empty and no error.
func Load(path string, getenv func(string) string) (*Config, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return Default(), nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading configuration: %w", err)
	}
	cfg, err := Parse(data, filepath.Dir(path), getenv)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	cfg.File = path
	return cfg, nil
}

// Parse decodes and validates configuration data. Relative paths are resolved
// against baseDir.
func Parse(data []byte, baseDir string, getenv func(string) string) (*Config, error) {
	var f file
	dec := toml.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&f); err != nil {
		var strict *toml.StrictMissingError
		if errors.As(err, &strict) {
			return nil, fmt.Errorf("unknown configuration field:\n%s", strict.String())
		}
		var de *toml.DecodeError
		if errors.As(err, &de) {
			row, col := de.Position()
			return nil, fmt.Errorf("line %d, column %d: %s", row, col, de.Error())
		}
		return nil, err
	}
	return build(&f, baseDir, getenv)
}

// builder turns the decoded file into a Config. Each section has its own
// method; all of them record errors instead of stopping at the first one, so
// that the user sees every problem at once.
type builder struct {
	cfg     *Config
	baseDir string
	getenv  func(string) string
	errs    []error
}

func (b *builder) errorf(format string, args ...any) {
	b.errs = append(b.errs, fmt.Errorf(format, args...))
}

func (b *builder) warnf(format string, args ...any) {
	b.cfg.Warnings = append(b.cfg.Warnings, fmt.Sprintf(format, args...))
}

func build(f *file, baseDir string, getenv func(string) string) (*Config, error) {
	b := &builder{cfg: Default(), baseDir: baseDir, getenv: getenv}
	b.version(f.Version)
	b.git(f.Git)
	b.branches(f.Branches)
	b.lazygit(f.Lazygit)
	for i, r := range f.Repositories {
		b.repository(i, r)
	}
	for i, d := range f.Discovery {
		b.discovery(i, d)
	}
	b.ui(f.UI)
	for i, l := range f.Launchers {
		b.launcher(i, l)
	}
	if len(b.errs) > 0 {
		return nil, errors.Join(b.errs...)
	}
	return b.cfg, nil
}

func (b *builder) version(v int) {
	if v == 0 {
		b.warnf("configuration has no version; assuming version 1")
	} else if v != CurrentVersion {
		b.errorf("unsupported configuration version %d (supported: %d)", v, CurrentVersion)
	}
}

func (b *builder) git(g fileGit) {
	cfg := &b.cfg.Git
	if g.Executable != "" {
		p, err := expandCommand(g.Executable, b.baseDir, b.getenv)
		if err != nil {
			b.errorf("[git].executable: %v", err)
		}
		cfg.Executable = p
	}
	if g.InspectionParallelism != nil {
		if *g.InspectionParallelism < 1 {
			b.errorf("[git].inspection_parallelism must be at least 1")
		}
		cfg.InspectionParallelism = *g.InspectionParallelism
	}
	if g.FetchParallelism != nil {
		if *g.FetchParallelism < 1 {
			b.errorf("[git].fetch_parallelism must be at least 1")
		}
		cfg.FetchParallelism = *g.FetchParallelism
	}
	if g.InspectionTimeout != nil {
		cfg.InspectionTimeout = g.InspectionTimeout.Duration
	}
	if g.FetchTimeout != nil {
		cfg.FetchTimeout = g.FetchTimeout.Duration
	}
	if g.FetchPrune != nil {
		cfg.FetchPrune = *g.FetchPrune
	}
	if g.Untracked != "" {
		if !validUntracked(g.Untracked) {
			b.errorf("[git].untracked must be \"normal\" or \"no\", not %q", g.Untracked)
		}
		cfg.Untracked = g.Untracked
	}
}

func (b *builder) branches(fb fileBranches) {
	cfg := &b.cfg.Branches
	if fb.Enabled != nil {
		cfg.Enabled = *fb.Enabled
	}
	if fb.Pinned != nil {
		cfg.Pinned = *fb.Pinned
		if err := validateBranchNames(*fb.Pinned); err != nil {
			b.errorf("[branches].pinned: %v", err)
		}
	}
	for _, p := range fb.Ignore {
		if err := validatePattern(p); err != nil {
			b.errorf("[branches].ignore: %v", err)
		}
	}
	cfg.Ignore = fb.Ignore
	if fb.MaxAge != nil {
		cfg.MaxAge = fb.MaxAge.Duration
	}
}

func (b *builder) lazygit(l fileLazygit) {
	cfg := &b.cfg.Lazygit
	if l.Command != "" {
		p, err := expandCommand(l.Command, b.baseDir, b.getenv)
		if err != nil {
			b.errorf("[lazygit].command: %v", err)
		}
		cfg.Command = p
	}
	cfg.Args = l.Args
	if l.Enabled != nil {
		cfg.Enabled = *l.Enabled
	}
}

func (b *builder) repository(i int, r fileRepository) {
	if r.Path == "" {
		b.errorf("repositories[%d]: path is required", i)
		return
	}
	p, err := expandPath(r.Path, b.baseDir, b.getenv)
	if err != nil {
		b.errorf("repositories[%d].path: %v", i, err)
		return
	}
	if r.Untracked != "" && !validUntracked(r.Untracked) {
		b.errorf("repositories[%d].untracked must be \"normal\" or \"no\", not %q", i, r.Untracked)
	}
	if r.RemoteName != "" && r.Remote == "" {
		b.warnf("repositories[%d]: remote_name has no effect without remote", i)
	}
	var pinned []string
	if r.PinnedBranches != nil {
		pinned = append([]string{}, *r.PinnedBranches...)
		if err := validateBranchNames(pinned); err != nil {
			b.errorf("repositories[%d].pinned_branches: %v", i, err)
		}
	}
	ws, err := expandWorkspace(r.Workspace, b.getenv)
	if err != nil {
		b.errorf("repositories[%d].workspace: %v", i, err)
	}
	enabled := true
	if r.Enabled != nil {
		enabled = *r.Enabled
	}
	b.cfg.Repositories = append(b.cfg.Repositories, Repository{
		Path: p, Name: strings.TrimSpace(r.Name), Family: r.Family, Workspace: ws, Enabled: enabled, Remote: r.Remote,
		RemoteName: r.RemoteName, Untracked: r.Untracked, PinnedBranches: pinned,
	})
}

func (b *builder) discovery(i int, d fileDiscovery) {
	if d.Root == "" {
		b.errorf("discovery[%d]: root is required", i)
		return
	}
	root, err := expandPath(d.Root, b.baseDir, b.getenv)
	if err != nil {
		b.errorf("discovery[%d].root: %v", i, err)
		return
	}
	depth := DefaultMaxDepth
	if d.MaxDepth != nil {
		depth = *d.MaxDepth
		if depth < 1 {
			b.errorf("discovery[%d].max_depth must be at least 1", i)
		}
	}
	include := d.Include
	if len(include) == 0 {
		include = []string{"**"}
	}
	for _, p := range append(append([]string{}, include...), d.Exclude...) {
		if err := validatePattern(p); err != nil {
			b.errorf("discovery[%d]: %v", i, err)
		}
	}
	ws, err := expandWorkspace(d.Workspace, b.getenv)
	if err != nil {
		b.errorf("discovery[%d].workspace: %v", i, err)
	}
	b.cfg.Discovery = append(b.cfg.Discovery, Discovery{
		Root: root, MaxDepth: depth, Family: d.Family, Workspace: ws, Include: include,
		Exclude: d.Exclude, FollowSymlinks: d.FollowSymlinks,
	})
}

func (b *builder) ui(u fileUI) {
	cfg := &b.cfg.UI
	if u.Icons != "" {
		switch u.Icons {
		case "unicode", "nerd", "ascii":
			cfg.Icons = u.Icons
		default:
			b.errorf("[ui].icons must be \"unicode\", \"nerd\", or \"ascii\", not %q", u.Icons)
		}
	}
	if u.View != "" {
		switch u.View {
		case "list", "tree":
			cfg.View = u.View
		default:
			b.errorf("[ui].view must be \"list\" or \"tree\", not %q", u.View)
		}
	}
	if u.Sort != "" {
		if slices.Contains(SortOrders, u.Sort) {
			cfg.Sort = u.Sort
		} else {
			b.errorf("[ui].sort must be one of %s, not %q", strings.Join(SortOrders, ", "), u.Sort)
		}
	}
	if n := u.DetailCommits; n != nil {
		if *n < 1 || *n > MaxDetailCommits {
			b.errorf("[ui].detail_commits must be between 1 and %d, not %d", MaxDetailCommits, *n)
		} else {
			cfg.DetailCommits = *n
		}
	}
	if n := u.IncomingCommits; n != nil {
		if *n < 0 || *n > MaxIncomingCommits {
			b.errorf("[ui].incoming_commits must be between 0 and %d, not %d", MaxIncomingCommits, *n)
		} else {
			cfg.IncomingCommits = *n
		}
	}
	b.theme(u.Theme)
}

func (b *builder) theme(t fileTheme) {
	ui := &b.cfg.UI
	if t.Preset != "" {
		if p, ok := ThemePresets[t.Preset]; ok {
			ui.Theme, ui.LightTheme = p, nil
		} else if f, ok := ThemeFamilies[t.Preset]; ok {
			light := ThemePresets[f.Light]
			ui.Theme, ui.LightTheme = ThemePresets[f.Dark], &light
		} else {
			b.errorf("[ui.theme].preset must be one of %s, not %q", strings.Join(ThemePresetNames(), ", "), t.Preset)
		}
	}
	themes := []*Theme{&ui.Theme}
	if ui.LightTheme != nil {
		themes = append(themes, ui.LightTheme)
	}
	for _, c := range []struct {
		key   string
		val   string
		field func(*Theme) *string
	}{
		{"active_border", t.ActiveBorder, func(t *Theme) *string { return &t.ActiveBorder }},
		{"inactive_border", t.InactiveBorder, func(t *Theme) *string { return &t.InactiveBorder }},
		{"title", t.Title, func(t *Theme) *string { return &t.Title }},
		{"selected_background", t.SelectedBackground, func(t *Theme) *string { return &t.SelectedBackground }},
		{"selected_foreground", t.SelectedForeground, func(t *Theme) *string { return &t.SelectedForeground }},
		{"selected_accent", t.SelectedAccent, func(t *Theme) *string { return &t.SelectedAccent }},
		{"header_background", t.HeaderBackground, func(t *Theme) *string { return &t.HeaderBackground }},
		{"header_foreground", t.HeaderForeground, func(t *Theme) *string { return &t.HeaderForeground }},
		{"key", t.Key, func(t *Theme) *string { return &t.Key }},
		{"branch", t.Branch, func(t *Theme) *string { return &t.Branch }},
		{"clean", t.Clean, func(t *Theme) *string { return &t.Clean }},
		{"dirty", t.Dirty, func(t *Theme) *string { return &t.Dirty }},
		{"ahead", t.Ahead, func(t *Theme) *string { return &t.Ahead }},
		{"behind", t.Behind, func(t *Theme) *string { return &t.Behind }},
		{"success", t.Success, func(t *Theme) *string { return &t.Success }},
		{"error", t.Error, func(t *Theme) *string { return &t.Error }},
		{"warning", t.Warning, func(t *Theme) *string { return &t.Warning }},
		{"dim", t.Dim, func(t *Theme) *string { return &t.Dim }},
	} {
		if c.val == "" {
			continue
		}
		if err := validateColor(c.val); err != nil {
			b.errorf("[ui.theme].%s: %v", c.key, err)
			continue
		}
		for _, th := range themes {
			*c.field(th) = c.val
		}
	}
}

func (b *builder) launcher(i int, l fileLauncher) {
	if l.Name == "" {
		b.errorf("launchers[%d]: name is required", i)
	}
	if len(l.Command) == 0 || l.Command[0] == "" {
		b.errorf("launchers[%d]: command must be a non-empty argument array", i)
		return
	}
	exe, err := expandCommand(l.Command[0], b.baseDir, b.getenv)
	if err != nil {
		b.errorf("launchers[%d].command: %v", i, err)
	}
	cmd := append([]string{exe}, l.Command[1:]...)
	for _, a := range cmd[1:] {
		for _, ph := range []string{"{path}", "{workspace}"} {
			if strings.Contains(a, ph) && a != ph {
				b.errorf("launchers[%d]: %s must be a complete argument, not part of %q", i, ph, a)
			}
		}
	}
	b.cfg.Launchers = append(b.cfg.Launchers, Launcher{Name: l.Name, Command: cmd})
}

func validUntracked(v string) bool { return v == "normal" || v == "no" }

// expandCommand expands a command that is either a bare executable name
// (looked up in PATH later) or a path.
func expandCommand(v, baseDir string, getenv func(string) string) (string, error) {
	if !strings.ContainsRune(v, '/') && !strings.HasPrefix(v, "~") && !strings.Contains(v, "${") {
		return v, nil
	}
	return expandPath(v, baseDir, getenv)
}

// expandPath expands a leading ~ and ${NAME} references and resolves relative
// paths against baseDir. Undefined variables are errors.
func expandPath(v, baseDir string, getenv func(string) string) (string, error) {
	expanded, err := expandVars(v, getenv)
	if err != nil {
		return "", err
	}
	if !filepath.IsAbs(expanded) {
		if baseDir == "" {
			wd, err := os.Getwd()
			if err != nil {
				return "", err
			}
			baseDir = wd
		}
		expanded = filepath.Join(baseDir, expanded)
	}
	return filepath.Clean(expanded), nil
}

// expandWorkspace expands variables and ~ but keeps a relative path relative;
// the inventory resolves it against the repository.
func expandWorkspace(v string, getenv func(string) string) (string, error) {
	if v == "" {
		return "", nil
	}
	expanded, err := expandVars(v, getenv)
	if err != nil {
		return "", err
	}
	return filepath.Clean(expanded), nil
}

// expandVars expands ${NAME} references and a leading ~.
func expandVars(v string, getenv func(string) string) (string, error) {
	if strings.Contains(v, "$") && !validVarSyntax(v) {
		return "", fmt.Errorf("%q: only ${NAME} variable references are supported", v)
	}
	var missing []string
	expanded := os.Expand(v, func(name string) string {
		val := getenv(name)
		if val == "" {
			missing = append(missing, name)
		}
		return val
	})
	if len(missing) > 0 {
		return "", fmt.Errorf("%q: undefined environment variable %s", v, strings.Join(missing, ", "))
	}
	if expanded == "~" || strings.HasPrefix(expanded, "~/") {
		home := getenv("HOME")
		if home == "" {
			return "", fmt.Errorf("%q: HOME is not set", v)
		}
		expanded = filepath.Join(home, strings.TrimPrefix(expanded, "~"))
	}
	return expanded, nil
}

// validVarSyntax accepts only ${NAME} forms.
func validVarSyntax(v string) bool {
	for i := 0; i < len(v); i++ {
		if v[i] != '$' {
			continue
		}
		if i+1 >= len(v) || v[i+1] != '{' {
			return false
		}
		end := strings.IndexByte(v[i:], '}')
		if end < 0 {
			return false
		}
		i += end
	}
	return true
}
