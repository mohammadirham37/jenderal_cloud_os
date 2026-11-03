package gpu

import (
	"os"
	"regexp"
	"strconv"
)

var containerIDRe = regexp.MustCompile(`[0-9a-f]{64}`)

// ContainerOfPID returns the Docker container ID (64 hex) a process belongs
// to, based on its cgroup path ("" when not in a container or unreadable).
func ContainerOfPID(pid int) string {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/cgroup")
	if err != nil {
		return ""
	}
	return ContainerIDFromCgroup(string(b))
}

// ContainerIDFromCgroup extracts a Docker container ID from cgroup contents
// (cgroup v2 "0::/system.slice/docker-<id>.scope" or v1 ".../docker/<id>").
func ContainerIDFromCgroup(s string) string {
	return containerIDRe.FindString(s)
}
