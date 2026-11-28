package app

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mmo-con/headquarters/internal/config"
	"github.com/mmo-con/headquarters/internal/domain"
	"github.com/mmo-con/headquarters/internal/git"
	"github.com/mmo-con/headquarters/internal/jobs"
)

func behindRepo() domain.Repository {
	return domain.Repository{
		ID: "r", StoreID: "s", Name: "r", Availability: domain.Available,
		Inspected: domain.InspectionInfo{FinishedAt: time.Now()},
		Inspection: domain.Inspection{
			Head:      domain.KnownValue(domain.HeadState{Branch: "main", CommitID: "abc"}),
			Operation: domain.KnownValue(domain.NoOperation),
			Worktree:  domain.KnownValue(domain.WorktreeState{Untracked: 3, UntrackedChecked: true}),
			Upstream:  domain.KnownValue(domain.UpstreamState{Configured: true, Ref: "origin/main", CommitID: "up1", Behind: 2}),
		},
	}
}

func TestEligibility(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(r *domain.Repository)
		busy   bool
		want   string
	}{
		{"eligible with untracked files", func(*domain.Repository) {}, false, ""},
		{"unavailable", func(r *domain.Repository) { r.Availability = domain.Missing }, false, "repository is missing"},
		{"unknown", func(r *domain.Repository) { r.Upstream = domain.Known[domain.UpstreamState]{} }, false, "state is unknown"},
		{"inspection error", func(r *domain.Repository) { r.Inspected.Err = errors.New("x") }, false, "state is unknown"},
		{"rebase", func(r *domain.Repository) { r.Operation.Value = domain.Rebasing }, false, "rebasing in progress"},
		{"dirty", func(r *domain.Repository) { r.Worktree.Value.Modified = 1 }, false, "worktree contains local changes"},
		{"staged", func(r *domain.Repository) { r.Worktree.Value.Staged = 1 }, false, "worktree contains local changes"},
		{"submodules behind", func(r *domain.Repository) { r.Worktree.Value.Modified, r.Worktree.Value.SubmoduleCommits = 2, 2 }, false,
			"submodules not updated; run git submodule update"},
		{"submodule and file", func(r *domain.Repository) { r.Worktree.Value.Modified, r.Worktree.Value.SubmoduleCommits = 2, 1 }, false,
			"worktree contains local changes"},
		{"submodule and staged file", func(r *domain.Repository) {
			r.Worktree.Value.Modified, r.Worktree.Value.SubmoduleCommits, r.Worktree.Value.Staged = 1, 1, 1
		}, false, "worktree contains local changes"},
		{"detached", func(r *domain.Repository) { r.Head.Value.Detached = true }, false, "detached HEAD"},
		{"unborn", func(r *domain.Repository) { r.Head.Value.Unborn = true }, false, "branch has no commits"},
		{"no upstream", func(r *domain.Repository) { r.Upstream.Value.Configured = false }, false, "no upstream"},
		{"gone", func(r *domain.Repository) { r.Upstream.Value.Gone = true }, false, "upstream gone"},
		{"diverged", func(r *domain.Repository) { r.Upstream.Value.Ahead = 1 }, false, "diverged from upstream"},
		{"ahead", func(r *domain.Repository) { r.Upstream.Value = domain.UpstreamState{Configured: true, Ahead: 1} }, false, "ahead of upstream"},
		{"up to date", func(r *domain.Repository) { r.Upstream.Value.Behind = 0 }, false, "already up to date"},
		{"upstream commit unknown", func(r *domain.Repository) { r.Upstream.Value.CommitID = "" }, false, "upstream commit is unknown"},
		{"busy", func(*domain.Repository) {}, true, "another operation or lazygit session is active"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := behindRepo()
			tc.mutate(&r)
			v := Eligibility(r, tc.busy)
			if got := v.String(); got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
			if v.Eligible() != (tc.want == "") || v.UpToDate() != (tc.name == "up to date") {
				t.Fatalf("eligible=%v upToDate=%v", v.Eligible(), v.UpToDate())
			}
		})
	}
}

// fakeGit is called concurrently by scheduler workers.
type fakeGit struct {
	git.Interface
	mu         sync.Mutex
	inspection domain.Inspection
	merged     int
	target     string
	fetched    []string
	cleanup    git.BranchCleanup
	deleted    []string
	deleteErr  error
}

func (f *fakeGit) ReadBranchCleanup(context.Context, domain.Location, string) (git.BranchCleanup, error) {
	return f.cleanup, nil
}

func (f *fakeGit) DeleteBranchRef(_ context.Context, _ domain.Location, name, commit string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.deleteErr != nil {
		return f.deleteErr
	}
	f.deleted = append(f.deleted, name+"@"+commit)
	return nil
}

func (f *fakeGit) DeleteBranch(_ context.Context, _ domain.Location, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.deleteErr != nil {
		return f.deleteErr
	}
	f.deleted = append(f.deleted, name)
	return nil
}

func (f *fakeGit) Inspect(context.Context, domain.Location, git.InspectOptions) (domain.Inspection, error) {
	return f.inspection, nil
}

func (f *fakeGit) MergeFastForwardOnly(_ context.Context, _ domain.Location, target string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.merged++
	f.target = target
	return nil
}

func (f *fakeGit) Fetch(_ context.Context, loc domain.Location, _ git.FetchOptions) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.fetched = append(f.fetched, loc.WorktreeRoot)
	return nil
}

func runJobs(t *testing.T, js []jobs.Job) []jobs.Event {
	t.Helper()
	s := jobs.NewScheduler(map[jobs.Kind]int{jobs.KindFetch: 2, jobs.KindUpdate: 2})
	s.Submit(js)
	var final []jobs.Event
	for len(final) < len(js) {
		for _, e := range s.Events.Next() {
			if e.Result.Status.Final() {
				final = append(final, e)
			}
		}
	}
	return final
}

func TestUpdateJobRechecksBeforeMerge(t *testing.T) {
	r := behindRepo()
	f := &fakeGit{inspection: domain.Inspection{
		Head: r.Head, Operation: r.Operation, Upstream: r.Upstream,
		Worktree: domain.KnownValue(domain.WorktreeState{Modified: 1}),
	}}
	ev := runJobs(t, UpdateJobs(f, []domain.Repository{r}, []UpdateCandidate{{Repository: r}}, time.Second))
	if ev[0].Result.Status != domain.StatusSkipped || f.merged != 0 {
		t.Fatalf("changed repository must be skipped: %+v merged=%d", ev[0].Result, f.merged)
	}

	f.inspection.Worktree = r.Worktree
	ev = runJobs(t, UpdateJobs(f, []domain.Repository{r}, []UpdateCandidate{{Repository: r}}, time.Second))
	if ev[0].Result.Status != domain.StatusSucceeded || f.merged != 1 || f.target != "up1" {
		t.Fatalf("eligible repository must be merged to the previewed upstream commit: %+v merged=%d target=%q", ev[0].Result, f.merged, f.target)
	}
}

func TestUpdateJobSkipsStateChangedSincePlanning(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(in *domain.Inspection)
		want   string
	}{
		{"upstream moved", func(in *domain.Inspection) { in.Upstream.Value.CommitID, in.Upstream.Value.Behind = "up2", 3 }, "upstream moved"},
		{"upstream rewritten with the same distance", func(in *domain.Inspection) { in.Upstream.Value.CommitID = "up2" }, "upstream moved"},
		{"branch switched", func(in *domain.Inspection) { in.Head.Value.Branch = "release" }, "branch changed"},
		{"HEAD moved", func(in *domain.Inspection) { in.Head.Value.CommitID = "def" }, "HEAD moved"},
		{"upstream changed", func(in *domain.Inspection) { in.Upstream.Value.Ref = "fork/main" }, "upstream changed"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := behindRepo()
			f := &fakeGit{inspection: domain.Inspection{Head: r.Head, Operation: r.Operation, Upstream: r.Upstream, Worktree: r.Worktree}}
			tc.mutate(&f.inspection)
			ev := runJobs(t, UpdateJobs(f, []domain.Repository{r}, []UpdateCandidate{{Repository: r}}, time.Second))
			if ev[0].Result.Status != domain.StatusSkipped || f.merged != 0 ||
				!strings.Contains(ev[0].Result.Error.Error(), tc.want+" since the update was planned") {
				t.Fatalf("%+v merged=%d", ev[0].Result, f.merged)
			}
		})
	}
}

func TestFetchJobsDeduplicateStores(t *testing.T) {
	all := []domain.Repository{
		{ID: "main", StoreID: "s1", Availability: domain.Available, Location: domain.Location{WorktreeRoot: "/main"}},
		{ID: "wt", StoreID: "s1", Availability: domain.Available, Location: domain.Location{WorktreeRoot: "/wt"}},
		{ID: "other", StoreID: "s2", Availability: domain.Available, Location: domain.Location{WorktreeRoot: "/other"}},
		{ID: "missing", Availability: domain.Missing},
	}
	f := &fakeGit{}
	js := FetchJobs(f, all, []domain.Repository{all[1], all[0], all[2], all[3]}, git.FetchOptions{}, time.Second)
	if len(js) != 2 {
		t.Fatalf("got %d jobs, want 2", len(js))
	}
	if len(js[0].Repositories) != 2 || !js[0].Mutating {
		t.Fatalf("job must cover every worktree of the store: %+v", js[0].Repositories)
	}
	runJobs(t, js)
	if len(f.fetched) != 2 {
		t.Fatalf("fetched %v", f.fetched)
	}
}

func TestRefreshStoreFetchTimes(t *testing.T) {
	old, recent := time.Unix(100, 0), time.Unix(200, 0)
	repos := []domain.Repository{
		{StoreID: "s", Inspection: domain.Inspection{FetchHead: domain.KnownValue(&old)}},
		{StoreID: "s", Inspection: domain.Inspection{FetchHead: domain.KnownValue(&recent)}},
		{StoreID: "t", Inspection: domain.Inspection{FetchHead: domain.KnownValue[*time.Time](nil)}},
		{StoreID: "u"},
	}
	RefreshStoreFetchTimes(repos)
	if !repos[0].LastFetch.Value.Equal(recent) || !repos[1].LastFetch.Value.Equal(recent) {
		t.Fatal("newest FETCH_HEAD of the store must win")
	}
	if !repos[2].LastFetch.Valid || repos[2].LastFetch.Value != nil {
		t.Fatal("never fetched")
	}
	if repos[3].LastFetch.Valid {
		t.Fatal("unknown must stay unknown")
	}
}

func TestStateTokens(t *testing.T) {
	up := func(u domain.UpstreamState) *domain.Repository {
		return &domain.Repository{Availability: domain.Available, Inspection: domain.Inspection{
			Upstream: domain.KnownValue(u), Worktree: domain.KnownValue(domain.WorktreeState{})}}
	}
	// fetched returns a repository fetched at t (nil: never), with or
	// without a remote.
	fetched := func(t *time.Time, remote bool) *domain.Repository {
		r := &domain.Repository{Availability: domain.Available, LastFetch: domain.KnownValue(t)}
		r.Remotes = domain.KnownValue([]domain.Remote{})
		if remote {
			r.Remotes = domain.KnownValue([]domain.Remote{{Name: "origin", URL: "git@a:b.git"}})
		}
		return r
	}
	stale, recent := time.Now().Add(-8*24*time.Hour), time.Now().Add(-time.Hour)
	cases := []struct {
		token string
		match *domain.Repository
		miss  *domain.Repository
	}{
		{"is:clean", up(domain.UpstreamState{}), &domain.Repository{Inspection: domain.Inspection{Worktree: domain.KnownValue(domain.WorktreeState{Untracked: 1})}}},
		{"is:ahead", up(domain.UpstreamState{Configured: true, Ahead: 1}), up(domain.UpstreamState{Configured: true, Behind: 1})},
		{"is:diverged", up(domain.UpstreamState{Configured: true, Ahead: 1, Behind: 1}), up(domain.UpstreamState{Configured: true, Ahead: 1})},
		{"is:noupstream", up(domain.UpstreamState{Configured: true, Gone: true}), up(domain.UpstreamState{Configured: true})},
		{"is:error", &domain.Repository{Inspected: domain.InspectionInfo{Err: errors.New("x")}}, up(domain.UpstreamState{})},
		{"is:mismatch", &domain.Repository{ExpectedRemote: &domain.ExpectedRemote{URL: "a"},
			Inspection: domain.Inspection{Remotes: domain.KnownValue([]domain.Remote{{Name: "origin", URL: "b"}})}}, up(domain.UpstreamState{})},
		{"is:stashed", &domain.Repository{Inspection: domain.Inspection{Stashes: domain.KnownValue(1)}},
			&domain.Repository{Inspection: domain.Inspection{Stashes: domain.KnownValue(0)}}},
		{"is:stale", fetched(nil, true), fetched(nil, false)},
		{"is:stale", fetched(&stale, true), fetched(&recent, true)},
		{"is:unknown-token", nil, up(domain.UpstreamState{})},
	}
	for _, tc := range cases {
		f := Filter{Text: tc.token}
		if tc.match != nil && !f.Matches(tc.match, false) {
			t.Errorf("%s must match", tc.token)
		}
		if f.Matches(tc.miss, false) {
			t.Errorf("%s must not match", tc.token)
		}
	}
	if (Filter{Text: "is:behind"}).Matches(&domain.Repository{}, false) {
		t.Error("unknown state must not match a state token")
	}
}

func TestValidateFilter(t *testing.T) {
	for _, text := range []string{"", "api is:dirty", "family:Work is:stale", "is:marked"} {
		if err := ValidateFilter(text, true); err != nil {
			t.Errorf("%q: %v", text, err)
		}
	}
	for _, text := range []string{"is:dirt", "family:", "api family: is:dirty"} {
		if err := ValidateFilter(text, true); err == nil {
			t.Errorf("%q must be rejected", text)
		}
	}
	if err := ValidateFilter("is:marked", false); err == nil {
		t.Error("is:marked must be rejected without marks")
	}
}

func TestHealthNames(t *testing.T) {
	want := "unknown error critical dirty behind ahead attention ok"
	var got []string
	for h := HealthUnknown; h <= HealthOK; h++ {
		got = append(got, h.String())
	}
	if strings.Join(got, " ") != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestFilterAndSort(t *testing.T) {
	now := time.Now()
	older := now.Add(-time.Hour)
	repos := []*domain.Repository{
		{Name: "beta", Path: "/w/beta", Family: "Work", Availability: domain.Available,
			Inspection: domain.Inspection{
				Worktree:   domain.KnownValue(domain.WorktreeState{Modified: 1}),
				Upstream:   domain.KnownValue(domain.UpstreamState{Configured: true}),
				LastCommit: domain.KnownValue(&domain.CommitSummary{Time: older})}},
		{Name: "Alpha", Path: "/p/alpha", Family: "Personal", Availability: domain.Available,
			Inspection: domain.Inspection{
				Worktree:   domain.KnownValue(domain.WorktreeState{}),
				Upstream:   domain.KnownValue(domain.UpstreamState{Configured: true, Behind: 2}),
				LastCommit: domain.KnownValue(&domain.CommitSummary{Time: now})}},
		{Name: "gamma", Path: "/w/gamma", Family: "Work", Availability: domain.Missing},
	}
	match := func(f Filter) []string {
		var out []string
		for _, r := range repos {
			if f.Matches(r, r.Name == "gamma") {
				out = append(out, r.Name)
			}
		}
		return out
	}
	cases := []struct {
		filter Filter
		want   string
	}{
		{Filter{Text: "is:dirty"}, "beta"},
		{Filter{Text: "is:behind"}, "Alpha"},
		{Filter{Text: "is:unavailable"}, "gamma"},
		{Filter{Family: "Work"}, "beta gamma"},
		{Filter{Text: "A"}, "beta Alpha gamma"},
		{Filter{Text: "alp"}, "Alpha"},
		{Filter{Text: "/w/ is:dirty"}, "beta"},
		{Filter{Text: "is:marked"}, "gamma"},
		{Filter{Text: "is:clean is:dirty"}, ""},
		{Filter{Text: "family:work"}, "beta gamma"},
		{Filter{Text: "family:WORK is:dirty"}, "beta"},
		{Filter{Text: "family:wor"}, ""},
		{Filter{Text: "family:"}, ""},
	}
	for _, tc := range cases {
		if got := strings.Join(match(tc.filter), " "); got != tc.want {
			t.Errorf("%+v: got %q, want %q", tc.filter, got, tc.want)
		}
	}

	order := func(k SortKey) string {
		list := append([]*domain.Repository{}, repos...)
		Sort(list, k, nil)
		s := ""
		for _, r := range list {
			s += r.Name + " "
		}
		return s
	}
	for k, want := range map[SortKey]string{
		SortName:       "Alpha beta gamma ",
		SortFamily:     "Alpha beta gamma ",
		SortCommitTime: "Alpha beta gamma ",
		SortWorktree:   "gamma beta Alpha ",
		SortUpstream:   "gamma Alpha beta ",
	} {
		if got := order(k); got != want {
			t.Errorf("sort %s: got %q, want %q", k, got, want)
		}
	}
}

func TestSortByHealth(t *testing.T) {
	now := time.Now()
	healthy := func(name, path string, mutate func(r *domain.Repository)) *domain.Repository {
		r := behindRepo()
		r.ID, r.Name, r.Path = domain.RepositoryID(path), name, path
		r.Worktree.Value, r.Upstream.Value.Behind = domain.WorktreeState{UntrackedChecked: true}, 0
		if mutate != nil {
			mutate(&r)
		}
		return &r
	}
	fetchFailed := healthy("fetchfailed", "/f", nil)
	repos := []*domain.Repository{
		healthy("ok", "/ok", nil),
		healthy("attention", "/at", func(r *domain.Repository) { r.Stashes = domain.KnownValue(1) }),
		healthy("unknown", "/u", func(r *domain.Repository) { r.Upstream = domain.Known[domain.UpstreamState]{} }),
		healthy("pending", "/p", func(r *domain.Repository) { r.Inspected = domain.InspectionInfo{} }),
		healthy("ahead", "/a", func(r *domain.Repository) { r.Upstream.Value.Ahead = 1 }),
		healthy("behind", "/b", func(r *domain.Repository) { r.Upstream.Value.Behind = 1 }),
		healthy("dirty", "/d2", func(r *domain.Repository) { r.Worktree.Value.Modified = 1 }),
		healthy("Dirty", "/d1", func(r *domain.Repository) { r.Worktree.Value.Modified = 1 }),
		healthy("critical", "/c", func(r *domain.Repository) { r.Upstream.Value.Ahead, r.Upstream.Value.Behind = 1, 1 }),
		fetchFailed,
		healthy("missing", "/m", func(r *domain.Repository) { r.Availability = domain.Missing }),
	}
	last := map[domain.RepositoryID]*domain.OperationResult{
		fetchFailed.ID: {Operation: domain.OperationFetch, Status: domain.StatusFailed, Error: errors.New("x")},
	}
	Sort(repos, SortHealth, func(r *domain.Repository) Health { return HealthOf(r, last[r.ID], now) })
	var got []string
	for _, r := range repos {
		got = append(got, r.Path)
	}
	want := "/f /m /c /d1 /d2 /b /a /at /p /u /ok"
	if strings.Join(got, " ") != want {
		t.Fatalf("got %v, want %s", got, want)
	}
}

func TestHealthCountsOnlyFailedOperationsAsErrors(t *testing.T) {
	now := time.Now()
	r := behindRepo()
	r.Upstream.Value.Behind = 0
	r.Worktree.Value = domain.WorktreeState{UntrackedChecked: true}
	for _, tc := range []struct {
		op     domain.OperationType
		status domain.OperationStatus
		want   Health
	}{
		{domain.OperationFetch, domain.StatusCancelled, HealthOK},
		{domain.OperationUpdate, domain.StatusSkipped, HealthOK},
		{domain.OperationFetch, domain.StatusFailed, HealthError},
	} {
		last := &domain.OperationResult{Operation: tc.op, Status: tc.status, Error: errors.New("reason")}
		if got := HealthOf(&r, last, now); got != tc.want {
			t.Errorf("%s %s: health %s, want %s", tc.op, tc.status, got, tc.want)
		}
	}
}

func TestSortKeyNames(t *testing.T) {
	if len(config.SortOrders) != len(SortKeys) {
		t.Fatalf("config.SortOrders %v and SortKeys differ", config.SortOrders)
	}
	for i, name := range config.SortOrders {
		if k, ok := SortKeyNamed(name); !ok || k != SortKeys[i] {
			t.Errorf("%q must name %s", name, SortKeys[i])
		}
	}
	if _, ok := SortKeyNamed("size"); ok {
		t.Error("an unknown name must be rejected")
	}
}

func TestStatusDriverCancelsBatchesStartedAfterCancellation(t *testing.T) {
	s := jobs.NewScheduler(map[jobs.Kind]int{jobs.KindInspect: 1})
	defer s.CancelAll()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	d := newDriver(ctx, s)
	var status domain.OperationStatus
	d.submit(func(ev jobs.Event) { status = ev.Result.Status })([]jobs.Job{{Kind: jobs.KindInspect,
		Run: func(ctx context.Context) (any, error) {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(5 * time.Second):
				return nil, nil
			}
		}}})
	d.wait(func() bool { return status.Final() })
	if status != domain.StatusCancelled {
		t.Fatalf("status = %s, want cancelled", status)
	}
}

func TestBranchReportSanitizesNames(t *testing.T) {
	// Git refuses control characters in branch names, so the names are
	// constructed here; the report must not print them either way.
	b := branchReport(domain.BranchState{Name: "evil\x1b[2J", Upstream: "origin/\x07x", Behind: 1, CommitTime: time.Now()})
	if b.Name != "evil�[2J" || b.Upstream == nil || *b.Upstream != "origin/�x" {
		t.Errorf("name=%q upstream=%v", b.Name, b.Upstream)
	}
}
