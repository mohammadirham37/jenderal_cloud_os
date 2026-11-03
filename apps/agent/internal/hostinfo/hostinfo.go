// Package hostinfo collects host inventory and metrics.
package hostinfo

import (
	"context"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/host"
	"github.com/shirou/gopsutil/v4/load"
	"github.com/shirou/gopsutil/v4/mem"
	"github.com/shirou/gopsutil/v4/net"

	"github.com/itk-cloud/itk-cloud/packages/agentproto"
)

// Collector samples host metrics; it keeps the previous network counters to
// compute throughput.
type Collector struct {
	ExcludeMountPrefixes []string
	mu                   sync.Mutex
	prevRx, prevTx       uint64
	prevAt               time.Time
}

var physicalFS = map[string]bool{
	"ext4": true, "ext3": true, "xfs": true, "btrfs": true, "zfs": true, "vfat": true,
	"apfs": true, "hfs": true, "ntfs": true, "f2fs": true,
}

// Info returns static host information.
func Info(ctx context.Context) (agentproto.AgentInfo, error) {
	hi, err := host.InfoWithContext(ctx)
	if err != nil {
		return agentproto.AgentInfo{}, err
	}
	info := agentproto.AgentInfo{
		Hostname: hi.Hostname, OS: hi.OS, Platform: strings.TrimSpace(hi.Platform + " " + hi.PlatformVersion),
		Kernel: hi.KernelVersion, Arch: runtime.GOARCH, BootTime: time.Unix(int64(hi.BootTime), 0).UTC(),
	}
	if cis, err := cpu.InfoWithContext(ctx); err == nil && len(cis) > 0 {
		info.CPUModel = cis[0].ModelName
	}
	info.CPUCores, _ = cpu.CountsWithContext(ctx, false)
	info.CPUThreads, _ = cpu.CountsWithContext(ctx, true)
	if vm, err := mem.VirtualMemoryWithContext(ctx); err == nil {
		info.MemoryTotal = vm.Total
	}
	return info, nil
}

// Metrics returns a metrics snapshot (Docker status is filled by the caller).
func (c *Collector) Metrics(ctx context.Context) (agentproto.HostMetrics, error) {
	m := agentproto.HostMetrics{CollectedAt: time.Now().UTC()}
	// 500ms sampling window gives a stable instantaneous CPU reading.
	if per, err := cpu.PercentWithContext(ctx, 500*time.Millisecond, true); err == nil && len(per) > 0 {
		m.PerCore = per
		var sum float64
		for _, p := range per {
			sum += p
		}
		m.CPUPercent = sum / float64(len(per))
	}
	if l, err := load.AvgWithContext(ctx); err == nil {
		m.Load1, m.Load5, m.Load15 = l.Load1, l.Load5, l.Load15
	}
	if vm, err := mem.VirtualMemoryWithContext(ctx); err == nil {
		m.MemoryTotal, m.MemoryAvail = vm.Total, vm.Available
		m.MemoryUsed = vm.Total - vm.Available
	}
	if sw, err := mem.SwapMemoryWithContext(ctx); err == nil {
		m.SwapTotal, m.SwapUsed = sw.Total, sw.Used
	}
	if parts, err := disk.PartitionsWithContext(ctx, false); err == nil {
		seen := map[string]bool{}
		for _, p := range parts {
			if !physicalFS[p.Fstype] || seen[p.Mountpoint] || c.excluded(p.Mountpoint) {
				continue
			}
			seen[p.Mountpoint] = true
			if u, err := disk.UsageWithContext(ctx, p.Mountpoint); err == nil && u.Total > 0 {
				m.Disks = append(m.Disks, agentproto.DiskUsage{Mountpoint: p.Mountpoint, Fstype: p.Fstype, Total: u.Total, Used: u.Used, Free: u.Free})
			}
		}
	}
	if ios, err := net.IOCountersWithContext(ctx, true); err == nil {
		var rx, tx uint64
		for _, io := range ios {
			if io.Name == "lo" || strings.HasPrefix(io.Name, "lo") || strings.HasPrefix(io.Name, "veth") ||
				strings.HasPrefix(io.Name, "docker") || strings.HasPrefix(io.Name, "br-") {
				continue
			}
			rx += io.BytesRecv
			tx += io.BytesSent
		}
		c.mu.Lock()
		now := time.Now()
		if !c.prevAt.IsZero() && rx >= c.prevRx && tx >= c.prevTx {
			secs := now.Sub(c.prevAt).Seconds()
			if secs > 0 {
				m.NetRxBps = float64(rx-c.prevRx) / secs
				m.NetTxBps = float64(tx-c.prevTx) / secs
			}
		}
		c.prevRx, c.prevTx, c.prevAt = rx, tx, now
		c.mu.Unlock()
	}
	if up, err := host.UptimeWithContext(ctx); err == nil {
		m.UptimeSeconds = up
	}
	return m, nil
}

func (c *Collector) excluded(mp string) bool {
	for _, p := range c.ExcludeMountPrefixes {
		if p != "" && strings.HasPrefix(mp, p) {
			return true
		}
	}
	return strings.HasPrefix(mp, "/snap/") || strings.HasPrefix(mp, "/var/lib/docker/")
}

// AppArmorEnabled reports whether AppArmor is enabled on the host.
func AppArmorEnabled() bool {
	b, err := os.ReadFile("/sys/module/apparmor/parameters/enabled")
	return err == nil && strings.TrimSpace(string(b)) == "Y"
}
