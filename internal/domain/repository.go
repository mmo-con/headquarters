// Package domain contains the core models of headquarters. It does not depend
// on the TUI, on process execution, or on Git output formats.
package domain

import (
	"time"
)

// RepositoryID is the canonical worktree root of an entry.
type RepositoryID string

// StoreID is the canonical common Git directory shared by linked worktrees.
type StoreID string

// FamilyID names a group of repositories.
type FamilyID string

// UngroupedFamily is assigned to repositories without an explicit family.
const UngroupedFamily FamilyID = "Ungrouped"

// Known distinguishes "not determined" from a valid zero value.
type Known[T any] struct {
	Value T
	Valid bool
}

// KnownValue returns a valid Known.
func KnownValue[T any](v T) Known[T] { return Known[T]{Value: v, Valid: true} }

type InventorySource string

const (
	InventorySourceExplicit   InventorySource = "explicit"
	InventorySourceDiscovered InventorySource = "discovered"
)

type Availability int

const (
	Available Availability = iota
	Missing
	NotARepository
	NotWorktreeRoot
	BareRepository
	UntrustedOwner
	// ValidationFailed means that Git did not finish the validation, for
	// example because it crashed or timed out.
	ValidationFailed
)

func (a Availability) String() string {
	switch a {
	case Available:
		return "available"
	case Missing:
		return "missing"
	case NotARepository:
		return "not a repository"
	case NotWorktreeRoot:
		return "config error"
	case BareRepository:
		return "unsupported: bare repository"
	case UntrustedOwner:
		return "untrusted owner"
	case ValidationFailed:
		return "validation failed"
	}
	return "unknown"
}

type UntrackedMode string

const (
	UntrackedNormal UntrackedMode = "normal"
	UntrackedNo     UntrackedMode = "no"
)

// Location holds the paths resolved during inventory validation.
type Location struct {
	WorktreeRoot string
	GitDir       string
	CommonDir    string
	Markers      Markers
}

// Markers are resolved paths of files that indicate Git state.
type Markers struct {
	MergeHead      string
	RebaseMerge    string
	RebaseApply    string
	CherryPickHead string
	RevertHead     string
	BisectLog      string
	FetchHead      string
}

type ExpectedRemote struct {
	Name string
	URL  string
}

type Remote struct {
	Name string
	URL  string
}

type HeadState struct {
	Branch   string
	CommitID string
	Detached bool
	Unborn   bool
}

type OperationInProgress int

const (
	NoOperation OperationInProgress = iota
	Merging
	Rebasing
	CherryPicking
	Reverting
	Bisecting
)

func (o OperationInProgress) String() string {
	switch o {
	case Merging:
		return "merging"
	case Rebasing:
		return "rebasing"
	case CherryPicking:
		return "cherry-picking"
	case Reverting:
		return "reverting"
	case Bisecting:
		return "bisecting"
	}
	return ""
}

type WorktreeState struct {
	Staged    int
	Modified  int
	Deleted   int
	Renamed   int
	Conflicts int
	// SubmoduleCommits counts the submodules whose only change is that their
	// checked-out commit differs from the one recorded in HEAD, for example
	// after a fast-forward moved them; they are also counted as Modified.
	SubmoduleCommits int
	Untracked        int
	UntrackedChecked bool
}

// Clean reports whether the worktree has no entries at all. Untracked entries
// count as dirty.
func (w WorktreeState) Clean() bool {
	return !w.HasTrackedChanges() && w.Untracked == 0
}

// OnlySubmoduleCommits reports whether the only tracked changes are
// submodules checked out at another commit than the one recorded in HEAD.
func (w WorktreeState) OnlySubmoduleCommits() bool {
	return w.SubmoduleCommits > 0 && w.Modified == w.SubmoduleCommits &&
		w.Staged == 0 && w.Deleted == 0 && w.Renamed == 0 && w.Conflicts == 0
}

// HasTrackedChanges reports whether tracked files differ from HEAD.
func (w WorktreeState) HasTrackedChanges() bool {
	return w.Staged > 0 || w.Modified > 0 || w.Deleted > 0 || w.Renamed > 0 || w.Conflicts > 0
}

type UpstreamState struct {
	Configured bool
	Ref        string
	// CommitID is the commit the upstream points to, or "" if unknown.
	CommitID string
	Gone     bool
	Ahead    int
	Behind   int
}

func (u UpstreamState) Diverged() bool { return u.Ahead > 0 && u.Behind > 0 }

type CommitSummary struct {
	ID      string
	ShortID string
	Author  string
	Time    time.Time
	Subject string
}

type InspectionInfo struct {
	Generation uint64
	FinishedAt time.Time
	Err        error
}

// Inspection is the result of inspecting one worktree.
type Inspection struct {
	Head       Known[HeadState]
	Operation  Known[OperationInProgress]
	Worktree   Known[WorktreeState]
	Upstream   Known[UpstreamState]
	Remotes    Known[[]Remote]
	LastCommit Known[*CommitSummary]
	FetchHead  Known[*time.Time]
	// Stashes is the number of stash entries of the repository store, which
	// all its worktrees share.
	Stashes Known[int]
	// Branches lists all local branches. It stays invalid if the inspection
	// did not read other branches.
	Branches Known[[]BranchState]
}

// Repository is one dashboard entry.
type Repository struct {
	ID      RepositoryID
	StoreID StoreID
	Name    string
	Path    string
	Family  FamilyID
	// Workspace is the configured absolute directory for {workspace}; empty
	// means the worktree root.
	Workspace string
	Source    InventorySource
	// DiscoveryRoot is the root of the discovery that found a discovered
	// repository.
	DiscoveryRoot  string
	ExpectedRemote *ExpectedRemote
	Untracked      UntrackedMode
	BranchPolicy   BranchPolicy
	Availability   Availability
	EntryError     error
	Location       Location

	// Inspected describes the inspection that supplied the embedded state.
	Inspected InspectionInfo
	// Inspection is the state of the latest applied inspection. In a
	// Repository, Branches holds only the local branches other than HEAD
	// that need attention according to BranchPolicy.
	Inspection
	PrimaryRemote *Remote
	// LastFetch is the newest FetchHead among all entries of the same store.
	LastFetch Known[*time.Time]
}

// ApplyInspection stores an inspection result unless a newer generation has
// already been applied.
func (r *Repository) ApplyInspection(gen uint64, in Inspection, err error, at time.Time) bool {
	if gen < r.Inspected.Generation {
		return false
	}
	r.Inspected = InspectionInfo{Generation: gen, FinishedAt: at, Err: err}
	if in.Branches.Valid {
		in.Branches = KnownValue(r.BranchPolicy.Select(in.Branches.Value, at))
	}
	r.Inspection = in
	name := ""
	if r.ExpectedRemote != nil {
		name = r.ExpectedRemote.Name
	}
	r.PrimaryRemote = SelectPrimaryRemote(in.Remotes, name)
	return true
}

// SelectPrimaryRemote picks the configured remote, otherwise origin, otherwise
// the alphabetically first remote.
func SelectPrimaryRemote(remotes Known[[]Remote], configured string) *Remote {
	if !remotes.Valid || len(remotes.Value) == 0 {
		return nil
	}
	find := func(name string) *Remote {
		for i := range remotes.Value {
			if remotes.Value[i].Name == name {
				r := remotes.Value[i]
				return &r
			}
		}
		return nil
	}
	if configured != "" {
		if r := find(configured); r != nil {
			return r
		}
	}
	if r := find("origin"); r != nil {
		return r
	}
	first := remotes.Value[0]
	for _, r := range remotes.Value[1:] {
		if r.Name < first.Name {
			first = r
		}
	}
	return &first
}

// StaleFetchAge is the age after which the last fetch is stale.
const StaleFetchAge = 7 * 24 * time.Hour

// FetchStale reports whether a repository with remotes was never fetched or
// not within StaleFetchAge before now, so its upstream state may be outdated.
// It returns false if the fetch time or the remotes are unknown.
func (r *Repository) FetchStale(now time.Time) bool {
	if !r.LastFetch.Valid || !r.Remotes.Valid || len(r.Remotes.Value) == 0 {
		return false
	}
	return r.LastFetch.Value == nil || now.Sub(*r.LastFetch.Value) > StaleFetchAge
}

// RemoteMismatch reports whether the expected remote differs from the actual
// one. It returns false if no assertion is configured or the state is unknown.
func (r *Repository) RemoteMismatch() bool {
	if r.ExpectedRemote == nil || r.ExpectedRemote.URL == "" || !r.Remotes.Valid {
		return false
	}
	name := r.ExpectedRemote.Name
	if name == "" {
		name = "origin"
	}
	for _, rem := range r.Remotes.Value {
		if rem.Name == name {
			return NormalizeRemoteURL(rem.URL) != NormalizeRemoteURL(r.ExpectedRemote.URL)
		}
	}
	return true
}

// UnpublishedBranches counts the reported branches other than HEAD that hold
// unpublished work or are missing.
func (r *Repository) UnpublishedBranches() int {
	n := 0
	for _, b := range r.Branches.Value {
		if b.Missing || b.Unpublished() {
			n++
		}
	}
	return n
}

// IsAvailable reports whether the entry is a usable worktree.
func (r *Repository) IsAvailable() bool { return r.Availability == Available }

// WorkspaceDir returns the directory that replaces {workspace} in launchers.
func (r *Repository) WorkspaceDir() string {
	if r.Workspace != "" {
		return r.Workspace
	}
	return r.Location.WorktreeRoot
}
