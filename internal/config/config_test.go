package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestParseExample(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "examples", "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := Parse(data, "/etc/gd", env(map[string]string{"HOME": "/home/u"}))
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Repositories) != 4 || len(cfg.Discovery) != 2 || len(cfg.Launchers) != 3 {
		t.Fatalf("unexpected counts: %+v", cfg)
	}
	if cfg.Repositories[0].Path != "/home/u/devrepos/dotfiles" || !cfg.Repositories[0].Enabled {
		t.Errorf("repository 0: %+v", cfg.Repositories[0])
	}
	if cfg.Repositories[2].Enabled {
		t.Error("repository 2 must be disabled")
	}
	if cfg.Repositories[3].Untracked != "no" {
		t.Error("untracked override lost")
	}
	if cfg.Discovery[1].MaxDepth != 3 || cfg.Discovery[1].Include[0] != "**" {
		t.Errorf("discovery 1: %+v", cfg.Discovery[1])
	}
	if cfg.Git.FetchTimeout != 120*time.Second || cfg.Git.InspectionTimeout != 10*time.Second || !cfg.Git.FetchPrune {
		t.Errorf("git: %+v", cfg.Git)
	}
}

func TestDefaultsWhenEmpty(t *testing.T) {
	cfg, err := Parse([]byte("version = 1\n"), "/x", env(nil))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Git.Executable != "git" || cfg.Git.InspectionParallelism != 8 || cfg.Git.FetchParallelism != 4 ||
		!cfg.Git.FetchPrune || cfg.Lazygit.Command != "lazygit" || !cfg.Lazygit.Enabled || cfg.Git.Untracked != "normal" {
		t.Fatalf("%+v", cfg)
	}
}

func TestRejectsInvalidConfiguration(t *testing.T) {
	tests := map[string]string{
		"unknown field":       "version = 1\n[[repositories]]\npath = \"/a\"\nfamliy = \"x\"\n",
		"wrong type":          "version = 1\n[git]\nfetch_parallelism = \"four\"\n",
		"bad version":         "version = 2\n",
		"missing path":        "version = 1\n[[repositories]]\nfamily = \"x\"\n",
		"bad untracked":       "version = 1\n[git]\nuntracked = \"all\"\n",
		"bad duration":        "version = 1\n[git]\nfetch_timeout = \"soon\"\n",
		"bad parallelism":     "version = 1\n[git]\ninspection_parallelism = 0\n",
		"undefined variable":  "version = 1\n[[repositories]]\npath = \"${NOPE}/x\"\n",
		"plain variable":      "version = 1\n[[repositories]]\npath = \"$HOME/x\"\n",
		"partial {path}":      "version = 1\n[[launchers]]\nname = \"x\"\ncommand = [\"x\", \"--dir={path}\"]\n",
		"partial {workspace}": "version = 1\n[[launchers]]\nname = \"x\"\ncommand = [\"x\", \"--dir={workspace}\"]\n",
		"empty launcher":      "version = 1\n[[launchers]]\nname = \"x\"\ncommand = []\n",
		"bad pattern":         "version = 1\n[[discovery]]\nroot = \"/r\"\nexclude = [\"[\"]\n",
		"bad depth":           "version = 1\n[[discovery]]\nroot = \"/r\"\nmax_depth = 0\n",
		"bad icons":           "version = 1\n[ui]\nicons = \"emoji\"\n",
		"bad view":            "version = 1\n[ui]\nview = \"grid\"\n",
		"no detail commits":   "version = 1\n[ui]\ndetail_commits = 0\n",
		"many detail commits": "version = 1\n[ui]\ndetail_commits = 21\n",
		"negative incoming":   "version = 1\n[ui]\nincoming_commits = -1\n",
		"many incoming":       "version = 1\n[ui]\nincoming_commits = 51\n",
		"bad color":           "version = 1\n[ui.theme]\nactive_border = \"greenish\"\n",
		"color out of range":  "version = 1\n[ui.theme]\ndim = \"256\"\n",
		"unknown preset":      "version = 1\n[ui.theme]\npreset = \"solarized\"\n",
		"bad branch age":      "version = 1\n[branches]\nmax_age = \"a month\"\n",
		"negative age":        "version = 1\n[branches]\nmax_age = \"-3d\"\n",
		"bad branch ignore":   "version = 1\n[branches]\nignore = [\"[\"]\n",
		"full ref pinned":     "version = 1\n[[repositories]]\npath = \"/a\"\npinned_branches = [\"refs/heads/main\"]\n",
	}
	for name, data := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := Parse([]byte(data), "/x", env(map[string]string{"HOME": "/h"})); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

func TestUISort(t *testing.T) {
	if cfg, err := Parse([]byte("version = 1\n"), "/x", env(nil)); err != nil || cfg.UI.Sort != "name" {
		t.Fatalf("default sort = %q, %v; want name", cfg.UI.Sort, err)
	}
	for _, name := range SortOrders {
		cfg, err := Parse([]byte("version = 1\n[ui]\nsort = \""+name+"\"\n"), "/x", env(nil))
		if err != nil || cfg.UI.Sort != name {
			t.Errorf("sort = %q: got %q, %v", name, cfg.UI.Sort, err)
		}
	}
	_, err := Parse([]byte("version = 1\n[ui]\nsort = \"size\"\n"), "/x", env(nil))
	if err == nil || !strings.Contains(err.Error(), "[ui].sort") || !strings.Contains(err.Error(), "size") {
		t.Fatalf("an unknown sort order must name the key and value: %v", err)
	}
}

func TestUnknownFieldIsNamed(t *testing.T) {
	_, err := Parse([]byte("version = 1\n[git]\nexecutabel = \"git\"\n"), "/x", env(nil))
	if err == nil || !strings.Contains(err.Error(), "executabel") {
		t.Fatalf("error must name the field: %v", err)
	}
}

func TestPathExpansion(t *testing.T) {
	e := env(map[string]string{"HOME": "/home/u", "DEV": "/srv/dev"})
	tests := map[string]string{
		"~":                "/home/u",
		"~/repos":          "/home/u/repos",
		"${DEV}/api":       "/srv/dev/api",
		"${HOME}/a/../b":   "/home/u/b",
		"relative/repo":    "/cfg/relative/repo",
		"/abs/olute":       "/abs/olute",
		"~user-is-literal": "/cfg/~user-is-literal",
	}
	for in, want := range tests {
		got, err := expandPath(in, "/cfg", e)
		if err != nil || got != want {
			t.Errorf("%q: got %q, %v; want %q", in, got, err, want)
		}
	}
	if got, _ := expandCommand("lazygit", "/cfg", e); got != "lazygit" {
		t.Errorf("bare command must stay a PATH lookup: %q", got)
	}
	if got, _ := expandCommand("~/bin/lazygit", "/cfg", e); got != "/home/u/bin/lazygit" {
		t.Errorf("command path: %q", got)
	}
}

func TestLocate(t *testing.T) {
	e := map[string]string{"HOME": "/home/u"}
	if p, _ := Locate("", env(e)); p != "/home/u/.config/headquarters/config.toml" {
		t.Errorf("default: %s", p)
	}
	e["XDG_CONFIG_HOME"] = "/xdg"
	if p, _ := Locate("", env(e)); p != "/xdg/headquarters/config.toml" {
		t.Errorf("xdg: %s", p)
	}
	e["HEADQUARTERS_CONFIG"] = "~/gd.toml"
	if p, _ := Locate("", env(e)); p != "/home/u/gd.toml" {
		t.Errorf("env: %s", p)
	}
	if p, _ := Locate("/flag.toml", env(e)); p != "/flag.toml" {
		t.Errorf("flag: %s", p)
	}
}

func TestLoadMissingFile(t *testing.T) {
	cfg, err := Load(filepath.Join(t.TempDir(), "none.toml"), env(nil))
	if err != nil || cfg.File != "" {
		t.Fatalf("cfg=%+v err=%v", cfg, err)
	}
}

func TestUISettings(t *testing.T) {
	cfg, err := Parse([]byte("version = 1\n[ui]\nicons = \"nerd\"\nview = \"tree\"\ndetail_commits = 5\nincoming_commits = 0\n[ui.theme]\nactive_border = \"#ff8800\"\nselected_background = \"236\"\nselected_accent = \"#ff8800\"\nbranch = \"bright-blue\"\nheader_background = \"magenta\"\nheader_foreground = \"black\"\n"), "/x", env(nil))
	if err != nil {
		t.Fatal(err)
	}
	th := cfg.UI.Theme
	if cfg.UI.Icons != "nerd" || cfg.UI.View != "tree" || cfg.UI.DetailCommits != 5 || cfg.UI.IncomingCommits != 0 || th.ActiveBorder != "#ff8800" || th.SelectedBackground != "236" || th.SelectedAccent != "#ff8800" || th.Branch != "bright-blue" ||
		th.HeaderBackground != "magenta" || th.HeaderForeground != "black" {
		t.Fatalf("%+v", cfg.UI)
	}
	if th.Dirty != DefaultTheme().Dirty {
		t.Error("unset colors must keep their defaults")
	}
	if ResolveColor("bright-blue") != "12" || ResolveColor("#ff8800") != "#ff8800" || ResolveColor("Gray") != "8" {
		t.Error("color resolution")
	}
}

func TestThemePresets(t *testing.T) {
	cfg, err := Parse([]byte("version = 1\n[ui.theme]\npreset = \"dracula\"\nerror = \"red\"\n"), "/x", env(nil))
	if err != nil {
		t.Fatal(err)
	}
	if th := cfg.UI.Theme; th.ActiveBorder != ThemePresets["dracula"].ActiveBorder || th.Error != "red" {
		t.Fatalf("a preset must apply and single colors must override it: %+v", th)
	}
	for name, p := range ThemePresets {
		v := reflect.ValueOf(p)
		for i := range v.NumField() {
			if err := validateColor(v.Field(i).String()); err != nil {
				t.Errorf("preset %s, %s: %v", name, v.Type().Field(i).Name, err)
			}
		}
	}
}

func TestOneDarkAndMonokaiPresets(t *testing.T) {
	for _, name := range []string{"onedark", "monokai"} {
		cfg, err := Parse([]byte("version = 1\n[ui.theme]\npreset = \""+name+"\"\n"), "/x", env(nil))
		if err != nil {
			t.Errorf("preset %s: %v", name, err)
			continue
		}
		if cfg.UI.Theme == DefaultTheme() {
			t.Errorf("preset %s must not be the default theme", name)
		}
	}
}

func TestThemeFamilyFollowsBackground(t *testing.T) {
	for family, v := range ThemeFamilies {
		cfg, err := Parse([]byte("version = 1\n[ui.theme]\npreset = \""+family+"\"\n"), "/x", env(nil))
		if err != nil {
			t.Errorf("family %s: %v", family, err)
			continue
		}
		if cfg.UI.ThemeFor(true) != ThemePresets[v.Dark] || cfg.UI.ThemeFor(false) != ThemePresets[v.Light] {
			t.Errorf("family %s must use %s on dark and %s on light backgrounds", family, v.Dark, v.Light)
		}
	}
	if len(ThemeFamilies) == 0 {
		t.Error("no theme families")
	}
}

func TestThemeFamilyVariantsArePresets(t *testing.T) {
	for family, v := range ThemeFamilies {
		for _, name := range []string{v.Dark, v.Light} {
			if _, ok := ThemePresets[name]; !ok {
				t.Errorf("family %s: no preset %q", family, name)
			}
		}
		if _, ok := ThemePresets[family]; ok {
			t.Errorf("family %s shadows a preset of the same name", family)
		}
	}
}

func TestThemeFamilyOverridesApplyToBothVariants(t *testing.T) {
	cfg, err := Parse([]byte("version = 1\n[ui.theme]\npreset = \"gruvbox\"\nerror = \"red\"\n"), "/x", env(nil))
	if err != nil {
		t.Fatal(err)
	}
	for _, dark := range []bool{true, false} {
		th := cfg.UI.ThemeFor(dark)
		want := ThemePresets["gruvbox-light"]
		if dark {
			want = ThemePresets["gruvbox-dark"]
		}
		want.Error = "red"
		if th != want {
			t.Errorf("dark=%v: %+v, want %+v", dark, th, want)
		}
	}
}

func TestFixedThemeIgnoresBackground(t *testing.T) {
	for _, data := range []string{"version = 1\n", "version = 1\n[ui.theme]\npreset = \"dracula\"\n"} {
		cfg, err := Parse([]byte(data), "/x", env(nil))
		if err != nil {
			t.Fatal(err)
		}
		if cfg.UI.ThemeFor(false) != cfg.UI.Theme || cfg.UI.ThemeFor(true) != cfg.UI.Theme {
			t.Errorf("%q: a fixed theme must not depend on the background", data)
		}
	}
}

func TestUnknownPresetNamesFamilies(t *testing.T) {
	_, err := Parse([]byte("version = 1\n[ui.theme]\npreset = \"solarized\"\n"), "/x", env(nil))
	if err == nil || !strings.Contains(err.Error(), "catppuccin,") || !strings.Contains(err.Error(), "gruvbox,") {
		t.Fatalf("the error must list the families: %v", err)
	}
}

func TestBranchesSettings(t *testing.T) {
	cfg, err := Parse([]byte("version = 1\n[[repositories]]\npath = \"/a\"\n"), "/x", env(nil))
	if err != nil {
		t.Fatal(err)
	}
	b := cfg.Branches
	if !b.Enabled || b.MaxAge != 30*24*time.Hour || strings.Join(b.Pinned, ",") != "main,master" ||
		cfg.Repositories[0].PinnedBranches != nil {
		t.Fatalf("defaults: %+v %+v", b, cfg.Repositories[0])
	}

	data := "version = 1\n[branches]\nenabled = false\npinned = []\nignore = [\"archive/**\"]\nmax_age = \"0\"\n" +
		"[[repositories]]\npath = \"/a\"\npinned_branches = [\"main\", \"develop\"]\n" +
		"[[repositories]]\npath = \"/b\"\npinned_branches = []\n"
	cfg, err = Parse([]byte(data), "/x", env(nil))
	if err != nil {
		t.Fatal(err)
	}
	b = cfg.Branches
	if b.Enabled || b.MaxAge != 0 || len(b.Pinned) != 0 || b.Ignore[0] != "archive/**" {
		t.Fatalf("branches: %+v", b)
	}
	if r := cfg.Repositories; len(r[0].PinnedBranches) != 2 || r[1].PinnedBranches == nil {
		t.Fatalf("per-repository pinned branches: %+v", r)
	}

	cfg, err = Parse([]byte("version = 1\n[branches]\nmax_age = \"36h\"\n"), "/x", env(nil))
	if err != nil || cfg.Branches.MaxAge != 36*time.Hour {
		t.Fatalf("duration age: %+v %v", cfg, err)
	}
}

func TestWorkspace(t *testing.T) {
	data := "version = 1\n[[repositories]]\npath = \"/a\"\nworkspace = \"~/ws\"\n" +
		"[[discovery]]\nroot = \"/r\"\nworkspace = \"../\"\n"
	cfg, err := Parse([]byte(data), "/x", env(map[string]string{"HOME": "/h"}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Repositories[0].Workspace != "/h/ws" || cfg.Discovery[0].Workspace != ".." {
		t.Fatalf("workspace: %q %q", cfg.Repositories[0].Workspace, cfg.Discovery[0].Workspace)
	}
}
