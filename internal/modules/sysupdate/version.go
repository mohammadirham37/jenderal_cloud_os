package sysupdate

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/itk-cloud/itk-cloud/packages/agentproto"
)

// version is a parsed semantic version (MAJOR.MINOR.PATCH[-PRERELEASE]).
type version struct {
	major, minor, patch int
	pre                 []string
}

// parseVersion parses "1.2.3", "v1.2.3" or "1.2.3-rc.1". Build metadata and
// anything else (e.g. "dev") is rejected.
func parseVersion(s string) (version, bool) {
	s = strings.TrimPrefix(strings.TrimSpace(s), "v")
	if !agentproto.SemverRe.MatchString(s) {
		return version{}, false
	}
	core, pre, _ := strings.Cut(s, "-")
	parts := strings.Split(core, ".")
	var v version
	var err error
	if v.major, err = strconv.Atoi(parts[0]); err != nil {
		return version{}, false
	}
	if v.minor, err = strconv.Atoi(parts[1]); err != nil {
		return version{}, false
	}
	if v.patch, err = strconv.Atoi(parts[2]); err != nil {
		return version{}, false
	}
	if pre != "" {
		v.pre = strings.Split(pre, ".")
	}
	return v, true
}

func (v version) String() string {
	s := fmt.Sprintf("%d.%d.%d", v.major, v.minor, v.patch)
	if len(v.pre) > 0 {
		s += "-" + strings.Join(v.pre, ".")
	}
	return s
}

func cmpInt(a, b int) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// compare follows semver precedence: a release ranks above its pre-releases;
// numeric pre-release identifiers compare numerically and rank below
// alphanumeric ones.
func (v version) compare(o version) int {
	if c := cmpInt(v.major, o.major); c != 0 {
		return c
	}
	if c := cmpInt(v.minor, o.minor); c != 0 {
		return c
	}
	if c := cmpInt(v.patch, o.patch); c != 0 {
		return c
	}
	switch {
	case len(v.pre) == 0 && len(o.pre) == 0:
		return 0
	case len(v.pre) == 0:
		return 1
	case len(o.pre) == 0:
		return -1
	}
	for i := 0; i < len(v.pre) && i < len(o.pre); i++ {
		a, b := v.pre[i], o.pre[i]
		an, aErr := strconv.Atoi(a)
		bn, bErr := strconv.Atoi(b)
		switch {
		case aErr == nil && bErr == nil:
			if c := cmpInt(an, bn); c != 0 {
				return c
			}
		case aErr == nil:
			return -1
		case bErr == nil:
			return 1
		default:
			if c := strings.Compare(a, b); c != 0 {
				return c
			}
		}
	}
	return cmpInt(len(v.pre), len(o.pre))
}

// CompareVersions compares two version strings. ok is false when either
// cannot be parsed (e.g. a development build "dev").
func CompareVersions(a, b string) (cmp int, ok bool) {
	va, okA := parseVersion(a)
	vb, okB := parseVersion(b)
	if !okA || !okB {
		return 0, false
	}
	return va.compare(vb), true
}
