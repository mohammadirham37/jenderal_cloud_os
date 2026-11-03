package sysupdate

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// UserAgent identifies update checks to GitHub.
const UserAgent = "jenderal-cloud-os-updater"

const (
	defaultAPIBase = "https://api.github.com"
	cacheTTL       = 10 * time.Minute
	errorTTL       = 2 * time.Minute
	maxNotes       = 64 << 10
)

// Release is the latest published release of the update repository.
type Release struct {
	Version        string     `json:"version"`
	Tag            string     `json:"tag"`
	Name           string     `json:"name"`
	PublishedAt    *time.Time `json:"published_at"`
	Notes          string     `json:"notes"`
	HTMLURL        string     `json:"html_url"`
	Asset          string     `json:"asset"`
	AssetAvailable bool       `json:"asset_available"`
}

// AssetName is the release tarball name produced by `make release`.
func AssetName(ver, arch string) string {
	return fmt.Sprintf("itk-cloud-%s-linux-%s.tar.gz", ver, arch)
}

type ghAsset struct {
	Name string `json:"name"`
	Size int64  `json:"size"`
}

type ghRelease struct {
	TagName     string     `json:"tag_name"`
	Name        string     `json:"name"`
	Body        string     `json:"body"`
	HTMLURL     string     `json:"html_url"`
	Draft       bool       `json:"draft"`
	Prerelease  bool       `json:"prerelease"`
	PublishedAt *time.Time `json:"published_at"`
	Assets      []ghAsset  `json:"assets"`
}

// hasAsset reports whether the release carries the tarball for arch and its
// .sha256 checksum file (the updater needs both).
func hasAsset(assets []ghAsset, ver, arch string) bool {
	name := AssetName(ver, arch)
	var tarball, sum bool
	for _, a := range assets {
		switch a.Name {
		case name:
			tarball = a.Size > 0
		case name + ".sha256":
			sum = true
		}
	}
	return tarball && sum
}

// toRelease converts the GitHub API response. Only tags of the form
// v<semver> are accepted, because the updater downloads from
// releases/download/v<version>/.
func toRelease(gr ghRelease, arch string) (*Release, error) {
	if gr.Draft || gr.Prerelease {
		return nil, fmt.Errorf("latest release %q is a draft or pre-release", gr.TagName)
	}
	if !strings.HasPrefix(gr.TagName, "v") {
		return nil, fmt.Errorf("release tag %q is not of the form vX.Y.Z", gr.TagName)
	}
	v, ok := parseVersion(gr.TagName)
	if !ok {
		return nil, fmt.Errorf("release tag %q is not a semantic version", gr.TagName)
	}
	ver := v.String()
	if "v"+ver != gr.TagName {
		return nil, fmt.Errorf("release tag %q is not canonical", gr.TagName)
	}
	notes := gr.Body
	if len(notes) > maxNotes {
		notes = notes[:maxNotes] + "\n…"
	}
	html := gr.HTMLURL
	if !strings.HasPrefix(html, "https://github.com/") {
		html = ""
	}
	name := strings.TrimSpace(gr.Name)
	if name == "" {
		name = gr.TagName
	}
	return &Release{Version: ver, Tag: gr.TagName, Name: name, PublishedAt: gr.PublishedAt, Notes: notes,
		HTMLURL: html, Asset: AssetName(ver, arch), AssetAvailable: hasAsset(gr.Assets, ver, arch)}, nil
}

// Checker fetches and caches the latest release of a GitHub repository.
type Checker struct {
	Repo    string
	Arch    string
	HTTP    *http.Client
	APIBase string // default https://api.github.com (tests override)

	mu        sync.Mutex
	release   *Release
	checkedAt time.Time
	err       string
	nextCheck time.Time
}

// Snapshot is the cached check result.
type Snapshot struct {
	Release   *Release
	CheckedAt time.Time
	Err       string
}

// Latest returns the cached release, refreshing it when the cache expired
// or force is set. Network errors keep the last known release and are
// reported in Snapshot.Err (servers without internet access keep working).
func (c *Checker) Latest(ctx context.Context, force bool) Snapshot {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now()
	if force || now.After(c.nextCheck) {
		rel, err := c.fetch(ctx)
		c.checkedAt = now
		if err != nil {
			c.err = err.Error()
			c.nextCheck = now.Add(errorTTL)
		} else {
			c.release, c.err = rel, ""
			c.nextCheck = now.Add(cacheTTL)
		}
	}
	s := Snapshot{Release: c.release, CheckedAt: c.checkedAt, Err: c.err}
	return s
}

func (c *Checker) fetch(ctx context.Context) (*Release, error) {
	base := c.APIBase
	if base == "" {
		base = defaultAPIBase
	}
	hc := c.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 10 * time.Second}
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/repos/"+c.Repo+"/releases/latest", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", UserAgent)
	resp, err := hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("cannot reach GitHub (is the server online?): %w", unwrapURLError(err))
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return nil, fmt.Errorf("no published release found in %s", c.Repo)
	case resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusTooManyRequests:
		return nil, fmt.Errorf("GitHub rate limit reached; try again later")
	case resp.StatusCode != http.StatusOK:
		return nil, fmt.Errorf("GitHub returned HTTP %d", resp.StatusCode)
	}
	var gr ghRelease
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&gr); err != nil {
		return nil, fmt.Errorf("invalid response from GitHub: %w", err)
	}
	return toRelease(gr, c.Arch)
}

func unwrapURLError(err error) error {
	type unwrapper interface{ Unwrap() error }
	if u, ok := err.(unwrapper); ok && u.Unwrap() != nil {
		return u.Unwrap()
	}
	return err
}
