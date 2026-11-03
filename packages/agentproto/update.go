package agentproto

import (
	"errors"
	"regexp"
	"strings"
)

// Platform self-update operations. The agent never downloads or executes
// anything itself: platform.update validates the request, writes a root-owned
// request file and starts the itk-cloud-updater.service oneshot unit, which
// downloads the release from the agent's configured GitHub repository,
// verifies its checksum and runs the release's own installer/upgrade.sh.
const (
	OpPlatformUpdate       = "platform.update"
	OpPlatformUpdateStatus = "platform.update_status"
)

func init() {
	Operations[OpPlatformUpdate] = func() Payload { return &PlatformUpdate{} }
	Operations[OpPlatformUpdateStatus] = func() Payload { return &Empty{} }
}

var (
	// SemverRe matches a release version without the leading "v"
	// (MAJOR.MINOR.PATCH with an optional pre-release suffix).
	SemverRe = regexp.MustCompile(`^(0|[1-9][0-9]{0,5})\.(0|[1-9][0-9]{0,5})\.(0|[1-9][0-9]{0,5})(-[0-9A-Za-z]+(\.[0-9A-Za-z]+){0,4})?$`)
	// RepoRe matches a GitHub "owner/name" repository slug.
	RepoRe = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,100}/[A-Za-z0-9_.-]{1,100}$`)
)

// UpdateArchs are the architectures release tarballs are built for.
var UpdateArchs = map[string]bool{"amd64": true, "arm64": true}

// PlatformUpdate asks the agent to start the platform updater for a release.
type PlatformUpdate struct {
	Version string `json:"version"` // e.g. "0.1.6" (no leading v)
	Repo    string `json:"repo"`    // must equal the agent's UPDATE_REPO
	Arch    string `json:"arch"`    // must equal the agent's GOARCH
}

// Validate implements Payload.
func (p *PlatformUpdate) Validate() error {
	if !SemverRe.MatchString(p.Version) {
		return errors.New("invalid version")
	}
	if !ValidRepo(p.Repo) {
		return errors.New("invalid repository")
	}
	if !UpdateArchs[p.Arch] {
		return errors.New("unsupported architecture")
	}
	return nil
}

// ValidRepo reports whether repo is a safe GitHub "owner/name" slug (no
// path segments starting with a dot, so "." and ".." are rejected).
func ValidRepo(repo string) bool {
	if !RepoRe.MatchString(repo) {
		return false
	}
	for _, part := range strings.Split(repo, "/") {
		if strings.HasPrefix(part, ".") {
			return false
		}
	}
	return true
}

// Update job states written by the updater.
const (
	UpdateIdle        = "idle"
	UpdateQueued      = "queued"
	UpdateDownloading = "downloading"
	UpdateVerifying   = "verifying"
	UpdateUpgrading   = "upgrading"
	UpdateSucceeded   = "succeeded"
	UpdateFailed      = "failed"
)

// UpdateRunning reports whether state is a non-terminal update state.
func UpdateRunning(state string) bool {
	switch state {
	case UpdateQueued, UpdateDownloading, UpdateVerifying, UpdateUpgrading:
		return true
	}
	return false
}

// UpdateStatus is the updater state returned by platform.update_status (and
// platform.update after queueing).
type UpdateStatus struct {
	State       string   `json:"state"`
	Version     string   `json:"version,omitempty"`
	FromVersion string   `json:"from_version,omitempty"`
	Message     string   `json:"message,omitempty"`
	StartedAt   string   `json:"started_at,omitempty"`
	UpdatedAt   string   `json:"updated_at,omitempty"`
	FinishedAt  string   `json:"finished_at,omitempty"`
	UnitActive  bool     `json:"unit_active"`
	Log         []string `json:"log"`
}
