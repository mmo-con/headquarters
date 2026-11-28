package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mmo-con/headquarters/internal/app"
	tu "github.com/mmo-con/headquarters/internal/testutil"
)

func TestMakeReleaseTag(t *testing.T) {
	makePath, err := exec.LookPath("make")
	if err != nil {
		t.Skip("make is not installed")
	}
	makefile, err := filepath.Abs(filepath.Join("..", "..", "Makefile"))
	if err != nil {
		t.Fatal(err)
	}
	tu.IsolateGit(t)
	repo := tu.InitRepo(t, t.TempDir())
	makeTag := func(tag string) (string, error) {
		cmd := exec.Command(makePath, "-f", makefile, "tag", "TAG="+tag)
		cmd.Dir = repo
		out, err := cmd.CombinedOutput()
		return string(out), err
	}
	for _, invalid := range []string{"", "1.0.1", "v1.0", "v1.0.1-rc.1", "v1.0.1\nv1.0.2"} {
		if out, err := makeTag(invalid); err == nil || !strings.Contains(out, "Usage: make tag TAG=vMAJOR.MINOR.PATCH") {
			t.Errorf("make tag TAG=%q: err=%v, output=%s", invalid, err, out)
		}
	}
	if tags := tu.Git(t, repo, "tag", "--list"); tags != "" {
		t.Fatalf("invalid input created tags: %s", tags)
	}

	if out, err := makeTag("v1.0.1"); err != nil {
		t.Fatalf("make tag: %v\n%s", err, out)
	}
	ref := "refs/tags/v1.0.1"
	if got := tu.Git(t, repo, "for-each-ref", "--format=%(objecttype) %(contents:subject)", ref); got != "tag headquarters v1.0.1" {
		t.Errorf("unexpected tag type or message: %q", got)
	}
	head := tu.Git(t, repo, "rev-parse", "HEAD")
	tu.Commit(t, repo, "README", "next\n", "next commit")
	if out, err := makeTag("v1.0.1"); err == nil {
		t.Errorf("make tag unexpectedly accepted a duplicate: %s", out)
	}
	if got := tu.Git(t, repo, "rev-parse", ref+"^{commit}"); got != head {
		t.Errorf("tagged commit = %s, want %s", got, head)
	}
}

func runCLI(t *testing.T, env map[string]string, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code = run(args, &out, &errOut, func(k string) string { return env[k] })
	return code, out.String(), errOut.String()
}

func writeConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestVersionAndUsage(t *testing.T) {
	if code, out, _ := runCLI(t, nil, "--version"); code != 0 || out != "headquarters dev\n" {
		t.Fatalf("--version: code=%d out=%q", code, out)
	}
	if code, _, errOut := runCLI(t, nil, "--unknown"); code != 2 || !strings.Contains(errOut, "flag provided but not defined") {
		t.Fatalf("unknown flag: code=%d stderr=%q", code, errOut)
	}
	if code, _, errOut := runCLI(t, nil, "-h"); code != 0 || !strings.Contains(errOut, "usage: headquarters [options] [path]") {
		t.Fatalf("-h: code=%d stderr=%q", code, errOut)
	}
}

func TestConfigurationErrorsExitWithTwo(t *testing.T) {
	path := writeConfig(t, "[git]\nunknown_key = 1\n")
	code, _, errOut := runCLI(t, nil, "--config", path, "--check")
	if code != 2 || !strings.Contains(errOut, "invalid configuration") || !strings.Contains(errOut, "unknown_key") {
		t.Fatalf("code=%d stderr=%q", code, errOut)
	}
}

func TestUnusableGitExitsWithOne(t *testing.T) {
	path := writeConfig(t, "[git]\nexecutable = \"/nonexistent/git\"\n")
	if code, _, errOut := runCLI(t, nil, "--config", path, "--check"); code != 1 || errOut == "" {
		t.Fatalf("code=%d stderr=%q", code, errOut)
	}
}

func TestCheckPrintsInventoryAndWarnings(t *testing.T) {
	tu.IsolateGit(t)
	base := tu.Canonical(t, t.TempDir())
	repo := tu.InitRepo(t, filepath.Join(base, "api"))
	missing := filepath.Join(base, "missing")
	path := writeConfig(t, `
[[repositories]]
path = "`+repo+`"
family = "Work"

[[repositories]]
path = "`+missing+`"

[lazygit]
command = "/nonexistent/lazygit"

[[launchers]]
name = "Broken"
command = ["/nonexistent/editor"]
`)
	code, out, errOut := runCLI(t, nil, "--config", path, "--check")
	if code != 0 {
		t.Fatalf("code=%d stderr=%q", code, errOut)
	}
	for _, want := range []string{
		"configuration: " + path,
		"Work",
		"available",
		repo,
		"missing",
		missing,
		"warning: cannot start lazygit",
		"warning: launcher Broken",
		"2 repositories",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
}

func TestCheckWithoutConfigurationFile(t *testing.T) {
	tu.IsolateGit(t)
	dir := t.TempDir()
	code, out, _ := runCLI(t, map[string]string{"XDG_CONFIG_HOME": dir}, "--check")
	want := "no configuration file at " + filepath.Join(dir, "headquarters", "config.toml")
	if code != 0 || !strings.Contains(out, want) || !strings.Contains(out, "0 repositories") {
		t.Fatalf("code=%d out=%q", code, out)
	}
}

func TestScanPathReplacesConfiguredRepositories(t *testing.T) {
	tu.IsolateGit(t)
	base := tu.Canonical(t, t.TempDir())
	configured := tu.InitRepo(t, filepath.Join(base, "configured"))
	scanned := tu.InitRepo(t, filepath.Join(base, "scan", "team", "api"))
	path := writeConfig(t, `
[[repositories]]
path = "`+configured+`"
`)
	code, out, errOut := runCLI(t, nil, "--config", path, "--check", filepath.Join(base, "scan"))
	if code != 0 {
		t.Fatalf("code=%d stderr=%q", code, errOut)
	}
	for _, want := range []string{"configuration: " + path, "scan of " + filepath.Join(base, "scan"), scanned, "1 repositories"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, configured) {
		t.Errorf("configured repository must be replaced:\n%s", out)
	}

	t.Chdir(scanned)
	if code, out, _ := runCLI(t, map[string]string{"XDG_CONFIG_HOME": base}, "--check", "."); code != 0 || !strings.Contains(out, scanned) {
		t.Fatalf("relative path to a repository: code=%d out=%q", code, out)
	}
}

func TestInvalidScanPathExitsWithTwo(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "file")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{filepath.Join(dir, "missing")}, "no such file"},
		{[]string{file}, "is not a directory"},
		{[]string{dir, dir}, "at most one path"},
		{[]string{dir, "--check"}, "options must precede the path"},
	} {
		if code, _, errOut := runCLI(t, nil, tc.args...); code != 2 || !strings.Contains(errOut, tc.want) {
			t.Errorf("%v: code=%d stderr=%q", tc.args, code, errOut)
		}
	}
}

func TestInitPrintsAndWritesConfiguration(t *testing.T) {
	tu.IsolateGit(t)
	base := tu.Canonical(t, t.TempDir())
	src := filepath.Join(base, "src")
	api := tu.InitRepo(t, filepath.Join(src, "work", "api"))
	if err := os.Mkdir(filepath.Join(src, "locked"), 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(filepath.Join(src, "locked"), 0o755) })

	code, out, errOut := runCLI(t, nil, "init", src)
	if code != 0 || !strings.HasPrefix(out, "# Generated by: headquarters init "+src+"\n") || !strings.Contains(out, "family = 'work'") {
		t.Fatalf("code=%d stderr=%q\n%s", code, errOut, out)
	}
	if os.Geteuid() != 0 && !strings.Contains(errOut, "warning: discovery:") {
		t.Errorf("warning missing on stderr: %q", errOut)
	}

	path := filepath.Join(base, "config", "config.toml")
	if code, out, errOut := runCLI(t, nil, "init", "-o", path, src); code != 0 || out != "" {
		t.Fatalf("-o: code=%d stdout=%q stderr=%q", code, out, errOut)
	}
	if code, out, _ := runCLI(t, nil, "--config", path, "--check"); code != 0 || !strings.Contains(out, "work") || !strings.Contains(out, api) {
		t.Fatalf("--check of generated file: code=%d\n%s", code, out)
	}
	if code, _, errOut := runCLI(t, nil, "init", "-o", path, src); code != 1 || !strings.Contains(errOut, "--force") {
		t.Fatalf("existing file: code=%d stderr=%q", code, errOut)
	}
	if code, _, errOut := runCLI(t, nil, "init", "-o", path, "--force", src); code != 0 {
		t.Fatalf("--force: code=%d stderr=%q", code, errOut)
	}
}

func TestInitErrors(t *testing.T) {
	tu.IsolateGit(t)
	dir := t.TempDir()
	file := filepath.Join(dir, "file")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "out.toml")
	for _, tc := range []struct {
		args []string
		code int
		want string
	}{
		{[]string{"init"}, 2, "./init"},
		{[]string{"init", filepath.Join(dir, "missing")}, 2, "no such file"},
		{[]string{"init", file}, 2, "is not a directory"},
		{[]string{"init", dir, "-o", out}, 2, "options must precede the directories"},
		{[]string{"init", "--max-depth", "0", dir}, 2, "--max-depth must be at least 1"},
		{[]string{"init", "--config", file, dir}, 2, "flag provided but not defined"},
		{[]string{"--check", "init", dir}, 2, "init must be the first argument"},
		{[]string{"init", "-o", out, dir}, 1, "no repositories found"},
	} {
		if code, _, errOut := runCLI(t, nil, tc.args...); code != tc.code || !strings.Contains(errOut, tc.want) {
			t.Errorf("%v: code=%d stderr=%q", tc.args, code, errOut)
		}
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Errorf("file written without repositories: %v", err)
	}
}

func TestDirectoryNamedInitIsStillScanned(t *testing.T) {
	tu.IsolateGit(t)
	base := tu.Canonical(t, t.TempDir())
	repo := tu.InitRepo(t, filepath.Join(base, "init", "repo"))
	t.Chdir(base)
	if code, out, _ := runCLI(t, map[string]string{"XDG_CONFIG_HOME": base}, "--check", "./init"); code != 0 || !strings.Contains(out, repo) {
		t.Fatalf("code=%d out=%q", code, out)
	}
}

// statusRepos creates repositories for the status command and returns the
// configuration path and the upstream of the clone named clean:
//
//	Work/clean      clone of upstream, never fetched
//	Work/dirty      modified file
//	Personal/notes  credentials in the remote URL, control characters in the subject
//	Work/missing    missing directory
func statusRepos(t *testing.T) (config, upstream, clean string) {
	t.Helper()
	tu.IsolateGit(t)
	base := tu.Canonical(t, t.TempDir())
	upstream = tu.InitRepo(t, filepath.Join(base, "upstream"))
	clean = filepath.Join(base, "work", "clean")
	tu.Git(t, "", "clone", "-q", upstream, clean)
	dirty := tu.InitRepo(t, filepath.Join(base, "work", "dirty"))
	tu.WriteFile(t, filepath.Join(dirty, "README"), "changed\n")
	notes := tu.InitRepo(t, filepath.Join(base, "personal", "notes"))
	tu.Commit(t, notes, "a", "a\n", "evil \x1b]0;pwned\x07 subject")
	tu.Git(t, notes, "remote", "add", "origin", "https://user:secret@example.com/notes.git")
	config = writeConfig(t, `
[[repositories]]
path = "`+clean+`"
family = "Work"

[[repositories]]
path = "`+dirty+`"
family = "Work"

[[repositories]]
path = "`+notes+`"
family = "Personal"

[[repositories]]
path = "`+filepath.Join(base, "work", "missing")+`"
family = "Work"
`)
	return config, upstream, clean
}

type statusDoc struct {
	Schema       int              `json:"schema"`
	GeneratedAt  string           `json:"generated_at"`
	Summary      map[string]any   `json:"summary"`
	Repositories []map[string]any `json:"repositories"`
}

func statusJSON(t *testing.T, wantCode int, args ...string) statusDoc {
	t.Helper()
	code, out, errOut := runCLI(t, nil, append([]string{"status"}, args...)...)
	if code != wantCode {
		t.Fatalf("status %v: code=%d, want %d\nstderr=%s", args, code, wantCode, errOut)
	}
	var doc statusDoc
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("stdout is not one JSON document: %v\n%s", err, out)
	}
	return doc
}

func names(doc statusDoc) string {
	var out []string
	for _, r := range doc.Repositories {
		out = append(out, r["name"].(string))
	}
	return strings.Join(out, " ")
}

func entry(t *testing.T, doc statusDoc, name string) map[string]any {
	t.Helper()
	for _, r := range doc.Repositories {
		if r["name"] == name {
			return r
		}
	}
	t.Fatalf("no repository %q in %s", name, names(doc))
	return nil
}

// field returns the value at a dotted path, such as "upstream.behind".
func field(r map[string]any, path string) any {
	var v any = r
	for _, k := range strings.Split(path, ".") {
		m, ok := v.(map[string]any)
		if !ok {
			return nil
		}
		v = m[k]
	}
	return v
}

func TestStatusReportsEveryRepository(t *testing.T) {
	config, _, clean := statusRepos(t)
	doc := statusJSON(t, 0, "--config", config, "--json")
	if doc.Schema != 1 || doc.GeneratedAt == "" {
		t.Fatalf("schema=%d generated_at=%q", doc.Schema, doc.GeneratedAt)
	}
	if got := names(doc); got != "clean dirty missing notes" {
		t.Fatalf("repositories: %s", got)
	}
	// missing is unavailable, so its state is unknown.
	if doc.Summary["total"] != 4.0 || field(doc.Summary, "states.dirty") != 1.0 || field(doc.Summary, "states.unknown") != 1.0 {
		t.Errorf("summary: %v", doc.Summary)
	}
	r := entry(t, doc, "clean")
	for path, want := range map[string]any{
		"path": clean, "family": "Work", "availability": "available", "known": true,
		"remote.name": "origin",
		"last_fetch":  nil, "fetch": nil, "error": nil,
		// never fetched
		"health": "attention",
	} {
		if got := field(r, path); got != want {
			t.Errorf("clean.%s = %#v, want %#v", path, got, want)
		}
	}
	// branches is the only source of the other branches; a separate count
	// would have to agree with it.
	for _, e := range doc.Repositories {
		if _, ok := e["attention_branches"]; ok {
			t.Errorf("%v: attention_branches must not be reported", e["name"])
		}
	}
	d := entry(t, doc, "dirty")
	if field(d, "worktree.modified") != 1.0 || d["health"] != "dirty" {
		t.Errorf("dirty: %v", d)
	}
}

// wantFields checks the values at dotted paths of r; a nil value must be
// present as null.
func wantFields(t *testing.T, name string, r map[string]any, want map[string]any) {
	t.Helper()
	for path, v := range want {
		got := field(r, path)
		if got == nil && v == nil {
			parent, key := r, path
			if i := strings.LastIndex(path, "."); i >= 0 {
				parent, _ = field(r, path[:i]).(map[string]any)
				key = path[i+1:]
			}
			if _, ok := parent[key]; !ok {
				t.Errorf("%s.%s is absent, want null", name, path)
			}
			continue
		}
		if got != v {
			t.Errorf("%s.%s = %#v, want %#v", name, path, got, v)
		}
	}
}

func wantAbsent(t *testing.T, name string, r map[string]any, keys ...string) {
	t.Helper()
	for _, k := range keys {
		if _, ok := r[k]; ok {
			t.Errorf("%s.%s must not be reported", name, k)
		}
	}
}

func TestStatusGroupsCheckedOutBranchUnderHead(t *testing.T) {
	config, _, _ := statusRepos(t)
	r := entry(t, statusJSON(t, 0, "--config", config), "clean")
	wantFields(t, "clean", r, map[string]any{
		"head.state": "attached", "head.branch": "main",
		"head.commit.subject": "initial commit", "head.commit.author": "Test",
		"head.upstream.name": "origin/main", "head.upstream.gone": false,
		"head.upstream.ahead": 0.0, "head.upstream.behind": 0.0,
	})
	if field(r, "head.commit.hash") == nil || field(r, "head.commit.short_hash") == nil || field(r, "head.commit.time") == nil {
		t.Errorf("head.commit incomplete: %v", field(r, "head.commit"))
	}
	wantAbsent(t, "clean", r, "branch", "upstream", "last_commit")
}

func TestStatusUpstreamWithoutConfiguredUpstream(t *testing.T) {
	config, _, _ := statusRepos(t)
	r := entry(t, statusJSON(t, 0, "--config", config), "dirty")
	u, _ := field(r, "head.upstream").(map[string]any)
	if len(u) != 4 {
		t.Fatalf("head.upstream = %#v, want name, gone, ahead, and behind", field(r, "head.upstream"))
	}
	// Without an upstream, ahead and behind do not apply: null, not zero.
	wantFields(t, "dirty", r, map[string]any{
		"head.upstream.name": nil, "head.upstream.gone": false,
		"head.upstream.ahead": nil, "head.upstream.behind": nil,
	})
}

func TestStatusDetachedHead(t *testing.T) {
	config, _, clean := statusRepos(t)
	tu.Git(t, clean, "switch", "-q", "--detach")
	r := entry(t, statusJSON(t, 0, "--config", config), "clean")
	wantFields(t, "clean", r, map[string]any{"head.state": "detached", "head.branch": nil,
		"head.commit.subject": "initial commit"})
}

func TestStatusUnbornHead(t *testing.T) {
	tu.IsolateGit(t)
	repo := filepath.Join(tu.Canonical(t, t.TempDir()), "empty")
	tu.Git(t, "", "init", "-q", "-b", "main", repo)
	config := writeConfig(t, "[[repositories]]\npath = \""+repo+"\"\n")
	r := entry(t, statusJSON(t, 0, "--config", config), "empty")
	wantFields(t, "empty", r, map[string]any{"head.state": "unborn", "head.branch": "main", "head.commit": nil})
}

func TestStatusGroupsWorktreeState(t *testing.T) {
	config, _, _ := statusRepos(t)
	r := entry(t, statusJSON(t, 0, "--config", config), "dirty")
	wantFields(t, "dirty", r, map[string]any{
		"worktree.operation": "none", "worktree.staged": 0.0, "worktree.modified": 1.0,
		"worktree.deleted": 0.0, "worktree.renamed": 0.0, "worktree.conflicts": 0.0,
		"worktree.untracked": 0.0, "stashes": 0.0,
	})
	wantAbsent(t, "dirty", r, "counts", "operation")
	if w, _ := r["worktree"].(map[string]any); len(w) != 7 {
		t.Errorf("worktree = %v, want only the operation and the counts", w)
	}
}

func TestStatusSummaryNestsStateCounts(t *testing.T) {
	config, _, _ := statusRepos(t)
	doc := statusJSON(t, 0, "--config", config)
	if len(doc.Summary) != 3 || doc.Summary["total"] != 4.0 {
		t.Fatalf("summary = %v, want only total, states, and health", doc.Summary)
	}
	states, _ := doc.Summary["states"].(map[string]any)
	if len(states) != len(app.StatusStates()) || states["dirty"] != 1.0 || states["unknown"] != 1.0 {
		t.Errorf("summary.states = %v", states)
	}
	if h, _ := doc.Summary["health"].(map[string]any); h["error"] != 1.0 {
		t.Errorf("summary.health = %v", h)
	}
}

func states(r map[string]any) string {
	list, ok := r["states"].([]any)
	if !ok {
		return fmt.Sprintf("%#v", r["states"])
	}
	var out []string
	for _, s := range list {
		out = append(out, s.(string))
	}
	return strings.Join(out, " ")
}

func TestStatusListsStatesOfEachRepository(t *testing.T) {
	config, _, _ := statusRepos(t)
	doc := statusJSON(t, 0, "--config", config)
	for name, want := range map[string]string{
		// in the order of app.StatusStates
		"clean":   "clean stale",
		"dirty":   "dirty noupstream",
		"missing": "error unavailable unknown",
	} {
		if got := states(entry(t, doc, name)); got != want {
			t.Errorf("%s.states = %s, want %s", name, got, want)
		}
	}
}

func TestStatusStatesIncludeFailedFetch(t *testing.T) {
	config, _, _ := statusRepos(t)
	doc := statusJSON(t, 0, "--config", config, "--fetch", "--filter", "family:personal")
	if got := states(entry(t, doc, "notes")); !slices.Contains(strings.Fields(got), "error") {
		t.Errorf("notes.states = %s, want error after a failed fetch", got)
	}
}

func TestStatusSummaryCountsListedStates(t *testing.T) {
	config, _, _ := statusRepos(t)
	doc := statusJSON(t, 0, "--config", config)
	count := map[string]float64{}
	for _, r := range doc.Repositories {
		for _, s := range strings.Fields(states(r)) {
			count[s]++
		}
	}
	for s, n := range doc.Summary["states"].(map[string]any) {
		if n != count[s] {
			t.Errorf("summary.states.%s = %v, but %v entries list it", s, n, count[s])
		}
	}
}

func TestStatusNeverReportsUnknownStateAsClean(t *testing.T) {
	config, _, _ := statusRepos(t)
	r := entry(t, statusJSON(t, 0, "--config", config), "missing")
	if r["known"] != false || r["health"] != "error" || r["availability"] != "missing" || r["error"] == nil {
		t.Errorf("missing: %v", r)
	}
	for _, k := range []string{"head", "worktree", "stashes", "branches"} {
		if v, ok := r[k]; !ok || v != nil {
			t.Errorf("missing.%s = %#v, want null", k, v)
		}
	}
}

func TestStatusFilter(t *testing.T) {
	config, _, _ := statusRepos(t)
	for filter, want := range map[string]string{
		"is:dirty family:work": "dirty",
		"family:personal":      "notes",
		"is:unavailable":       "missing",
	} {
		doc := statusJSON(t, 0, "--config", config, "--filter", filter)
		if got := names(doc); got != want {
			t.Errorf("%q: got %q, want %q", filter, got, want)
		}
		if doc.Summary["total"] != float64(len(strings.Fields(want))) {
			t.Errorf("%q: summary counts unselected repositories: %v", filter, doc.Summary)
		}
	}
	for _, filter := range []string{"is:bogus", "is:marked", "family:"} {
		code, out, errOut := runCLI(t, nil, "status", "--config", config, "--filter", filter)
		if code != 2 || out != "" || !strings.Contains(errOut, "filter") {
			t.Errorf("%q: code=%d out=%q stderr=%q", filter, code, out, errOut)
		}
	}
}

func TestStatusNDJSON(t *testing.T) {
	config, _, _ := statusRepos(t)
	code, out, errOut := runCLI(t, nil, "status", "--config", config, "--format", "ndjson")
	if code != 0 {
		t.Fatalf("code=%d stderr=%s", code, errOut)
	}
	lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
	if len(lines) != 4 {
		t.Fatalf("want one line per repository:\n%s", out)
	}
	for _, line := range lines {
		var r map[string]any
		if err := json.Unmarshal([]byte(line), &r); err != nil || r["schema"] != 1.0 || r["name"] == nil {
			t.Errorf("line %q: %v %v", line, err, r)
		}
		if _, ok := r["branches"]; !ok {
			t.Errorf("line without branches: %q", line)
		}
	}
	if code, _, _ := runCLI(t, nil, "status", "--config", config, "--json", "--format", "ndjson"); code != 2 {
		t.Errorf("--json with --format ndjson: code=%d", code)
	}
}

func TestStatusFailOn(t *testing.T) {
	config, _, _ := statusRepos(t)
	statusJSON(t, 3, "--config", config, "--fail-on", "dirty")
	statusJSON(t, 3, "--config", config, "--fail-on", "behind,unavailable")
	statusJSON(t, 0, "--config", config, "--fail-on", "dirty,unknown", "--filter", "family:personal")
	if code, _, errOut := runCLI(t, nil, "status", "--config", config, "--fail-on", "dirty,bogus"); code != 2 || !strings.Contains(errOut, "bogus") {
		t.Errorf("invalid state: code=%d stderr=%q", code, errOut)
	}
}

func TestStatusFailOnUnknownMatchesUnreadableRepositories(t *testing.T) {
	tu.IsolateGit(t)
	repo := tu.InitRepo(t, filepath.Join(tu.Canonical(t, t.TempDir()), "broken"))
	tu.WriteFile(t, filepath.Join(repo, "README"), "changed\n")
	// git status cannot read a corrupt index, so the worktree state stays
	// unknown although the file was modified.
	tu.WriteFile(t, filepath.Join(repo, ".git", "index"), "garbage")
	config := writeConfig(t, "[[repositories]]\npath = \""+repo+"\"\n")
	doc := statusJSON(t, 3, "--config", config, "--fail-on", "dirty,unknown")
	if r := entry(t, doc, "broken"); r["known"] != false || field(doc.Summary, "states.unknown") != 1.0 {
		t.Errorf("known=%v summary=%v", r["known"], doc.Summary)
	}
}

func TestStatusFetchesOnlyWhenAsked(t *testing.T) {
	config, upstream, clean := statusRepos(t)
	fetchHead := filepath.Join(clean, ".git", "FETCH_HEAD")
	tu.Commit(t, upstream, "new", "new\n", "upstream commit")

	r := entry(t, statusJSON(t, 0, "--config", config), "clean")
	if _, err := os.Stat(fetchHead); !os.IsNotExist(err) || field(r, "head.upstream.behind") != 0.0 {
		t.Fatalf("status without --fetch fetched: %v, %v", err, r["head"])
	}
	statusJSON(t, 0, "--config", config, "--fetch", "--filter", "family:personal")
	if _, err := os.Stat(fetchHead); !os.IsNotExist(err) {
		t.Fatal("--fetch fetched a repository outside the filter")
	}

	doc := statusJSON(t, 3, "--config", config, "--fetch", "--fail-on", "error")
	r = entry(t, doc, "clean")
	if field(r, "head.upstream.behind") != 1.0 || field(r, "fetch.status") != "succeeded" || r["last_fetch"] == nil || r["health"] != "behind" {
		t.Errorf("clean after fetch: %v", r)
	}
	n := entry(t, doc, "notes")
	if field(n, "fetch.status") != "failed" || field(n, "fetch.error") == nil || n["health"] != "error" || n["error"] == nil {
		t.Errorf("failed fetch must be reported: %v", n)
	}
}

func TestStatusSanitizesRepositoryText(t *testing.T) {
	config, _, _ := statusRepos(t)
	code, out, _ := runCLI(t, nil, "status", "--config", config, "--filter", "family:personal")
	if code != 0 || strings.Contains(out, "secret") || strings.Contains(out, `\u001b`) || strings.Contains(out, `\u0007`) {
		t.Fatalf("code=%d out=%s", code, out)
	}
	var doc statusDoc
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatal(err)
	}
	r := entry(t, doc, "notes")
	if got := field(r, "remote.url"); got != "https://***@example.com/notes.git" {
		t.Errorf("remote.url = %v", got)
	}
}

// commitAt commits like tu.Commit with a fixed commit time, so that the
// order of branches by latest commit is deterministic.
func commitAt(t *testing.T, repo, file, message string, at time.Time) {
	t.Helper()
	date := at.Format(time.RFC3339)
	t.Setenv("GIT_COMMITTER_DATE", date)
	t.Setenv("GIT_AUTHOR_DATE", date)
	tu.Commit(t, repo, file, file+"\n", message)
	os.Unsetenv("GIT_COMMITTER_DATE")
	os.Unsetenv("GIT_AUTHOR_DATE")
}

// branchRepo creates a clone on branch work with one other branch per
// reason, plus branches that are not reported, and returns a configuration
// for it:
//
//	main      pinned, behind origin/main
//	release   pinned, missing
//	gone      upstream gone
//	local     no upstream
//	div       diverged from origin/d, latest commit two days ago
//	ahead     ahead of origin/a, latest commit three days ago
//	wt        checked out in a linked worktree, not reported
//	tmp/x     ignored, not reported
func branchRepo(t *testing.T, extra string) (config, repo string) {
	t.Helper()
	tu.IsolateGit(t)
	base := tu.Canonical(t, t.TempDir())
	now := time.Now()
	upstream := tu.InitRepo(t, filepath.Join(base, "upstream"))
	for _, b := range []string{"a", "d", "g"} {
		tu.Git(t, upstream, "branch", b)
	}
	repo = filepath.Join(base, "branches")
	tu.Git(t, "", "clone", "-q", upstream, repo)
	tu.Git(t, repo, "switch", "-q", "-c", "work")
	for _, b := range []string{"local", "tmp/x"} {
		tu.Git(t, repo, "branch", b)
	}
	tu.Git(t, repo, "worktree", "add", "-q", "-b", "wt", filepath.Join(base, "wt"))
	for b, up := range map[string]string{"ahead": "origin/a", "div": "origin/d", "gone": "origin/g"} {
		tu.Git(t, repo, "branch", "-q", "--track", b, up)
	}
	tu.Git(t, repo, "switch", "-q", "ahead")
	commitAt(t, repo, "ahead", "ahead commit", now.Add(-72*time.Hour))
	tu.Git(t, repo, "switch", "-q", "div")
	commitAt(t, repo, "div", "local div commit", now.Add(-48*time.Hour))
	tu.Git(t, repo, "switch", "-q", "work")
	tu.Git(t, upstream, "switch", "-q", "d")
	tu.Commit(t, upstream, "d", "d\n", "upstream d commit")
	tu.Git(t, upstream, "switch", "-q", "main")
	tu.Commit(t, upstream, "m", "m\n", "upstream main commit")
	tu.Git(t, upstream, "branch", "-q", "-D", "g")
	tu.Git(t, repo, "fetch", "-q", "--prune")
	config = writeConfig(t, `
[branches]
ignore = ["tmp/**"]
`+extra+`

[[repositories]]
path = "`+repo+`"
pinned_branches = ["main", "release"]
`)
	return config, repo
}

func branchEntries(t *testing.T, r map[string]any) []map[string]any {
	t.Helper()
	list, ok := r["branches"].([]any)
	if !ok {
		t.Fatalf("branches = %#v, want a list", r["branches"])
	}
	var out []map[string]any
	for _, v := range list {
		out = append(out, v.(map[string]any))
	}
	return out
}

func TestStatusReportsBranchReasons(t *testing.T) {
	config, _ := branchRepo(t, "")
	r := entry(t, statusJSON(t, 0, "--config", config), "branches")
	byName := map[string]map[string]any{}
	for _, b := range branchEntries(t, r) {
		byName[b["name"].(string)] = b
	}
	for name, want := range map[string]map[string]any{
		"main": {"state": "behind", "pinned": true, "upstream.name": "origin/main", "upstream.gone": false,
			"upstream.ahead": 0.0, "upstream.behind": 1.0},
		"release": {"state": "missing", "pinned": true, "upstream": nil, "commit_time": nil},
		"gone": {"state": "gone", "pinned": false, "upstream.name": "origin/g", "upstream.gone": true,
			"upstream.ahead": nil, "upstream.behind": nil},
		"local": {"state": "noupstream", "pinned": false, "upstream.name": nil, "upstream.gone": false,
			"upstream.ahead": nil, "upstream.behind": nil},
		"div":   {"state": "diverged", "pinned": false, "upstream.name": "origin/d", "upstream.ahead": 1.0, "upstream.behind": 1.0},
		"ahead": {"state": "ahead", "pinned": false, "upstream.name": "origin/a", "upstream.ahead": 1.0, "upstream.behind": 0.0},
	} {
		b, ok := byName[name]
		if !ok {
			t.Errorf("branch %s not reported: %v", name, r["branches"])
			continue
		}
		wantFields(t, name, b, want)
		wantAbsent(t, name, b, "ahead", "behind")
		if _, missing := want["commit_time"]; !missing {
			if ts, _ := b["commit_time"].(string); ts == "" {
				t.Errorf("%s.commit_time = %#v, want a time", name, b["commit_time"])
			} else if _, err := time.Parse(time.RFC3339, ts); err != nil {
				t.Errorf("%s.commit_time: %v", name, err)
			}
		}
	}
}

func TestStatusBranchesFollowDetailPanelOrder(t *testing.T) {
	config, _ := branchRepo(t, "")
	r := entry(t, statusJSON(t, 0, "--config", config), "branches")
	var got []string
	for _, b := range branchEntries(t, r) {
		got = append(got, b["name"].(string))
	}
	// Pinned branches in configuration order, then the others by latest
	// commit, newest first, ties by name.
	if want := "main release gone local div ahead"; strings.Join(got, " ") != want {
		t.Errorf("order = %v, want %s", got, want)
	}
}

func TestStatusBranchesExcludeWorktreeAndIgnoredBranches(t *testing.T) {
	config, _ := branchRepo(t, "")
	r := entry(t, statusJSON(t, 0, "--config", config), "branches")
	for _, b := range branchEntries(t, r) {
		switch b["name"] {
		case "work", "wt", "tmp/x":
			t.Errorf("branch %v must not be reported", b["name"])
		}
	}
}

func TestStatusReportsEmptyBranchList(t *testing.T) {
	config, _, _ := statusRepos(t)
	r := entry(t, statusJSON(t, 0, "--config", config), "clean")
	if list, ok := r["branches"].([]any); !ok || len(list) != 0 {
		t.Errorf("branches = %#v, want []", r["branches"])
	}
}

func TestStatusBranchesNullWhenBranchesDisabled(t *testing.T) {
	config, _ := branchRepo(t, "enabled = false")
	r := entry(t, statusJSON(t, 0, "--config", config), "branches")
	if v, ok := r["branches"]; !ok || v != nil {
		t.Errorf("branches = %#v, want null", v)
	}
}

func TestStatusGoneUpstreamHasNoCounts(t *testing.T) {
	tu.IsolateGit(t)
	base := tu.Canonical(t, t.TempDir())
	upstream := tu.InitRepo(t, filepath.Join(base, "upstream"))
	tu.Git(t, upstream, "branch", "g")
	repo := filepath.Join(base, "gone")
	tu.Git(t, "", "clone", "-q", upstream, repo)
	tu.Git(t, repo, "switch", "-q", "--track", "-c", "g", "origin/g")
	tu.Git(t, upstream, "branch", "-q", "-D", "g")
	tu.Git(t, repo, "fetch", "-q", "--prune")
	config := writeConfig(t, "[[repositories]]\npath = \""+repo+"\"\n")
	r := entry(t, statusJSON(t, 0, "--config", config), "gone")
	wantFields(t, "gone", r, map[string]any{"head.upstream.name": "origin/g", "head.upstream.gone": true,
		"head.upstream.ahead": nil, "head.upstream.behind": nil})
}
