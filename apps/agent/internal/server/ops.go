package server

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/itk-cloud/itk-cloud/apps/agent/internal/backup"
	"github.com/itk-cloud/itk-cloud/apps/agent/internal/docker"
	"github.com/itk-cloud/itk-cloud/apps/agent/internal/fsops"
	"github.com/itk-cloud/itk-cloud/apps/agent/internal/gpu"
	"github.com/itk-cloud/itk-cloud/apps/agent/internal/hostinfo"
	"github.com/itk-cloud/itk-cloud/apps/agent/internal/native"
	"github.com/itk-cloud/itk-cloud/packages/agentproto"
)

var errNoSpace = errors.New("no space left")

func isNoSpace(err error) bool {
	return errors.Is(err, syscall.ENOSPC) || errors.Is(err, syscall.EDQUOT)
}

// Labels applied to every managed container.
const (
	labelManaged   = "itk.managed"
	labelWorkspace = "itk.workspace_id"
	labelContainer = "itk.container_id"
	labelOwner     = "itk.owner_id"
)

func (s *Server) dispatch(ctx context.Context, req *agentproto.Request, p agentproto.Payload) (any, error) {
	switch req.Operation {
	case agentproto.OpAgentInfo:
		return s.agentInfo(ctx)
	case agentproto.OpHostMetrics:
		return s.hostMetrics(ctx)
	case agentproto.OpGPUInventory:
		inv := gpu.Inventory(ctx, s.cfg.NvidiaSMI)
		s.attributeGPUProcesses(ctx, &inv)
		return inv, nil
	case agentproto.OpWorkspaceProvision:
		wp := p.(*agentproto.WorkspaceProvision)
		if wp.Native && !s.cfg.NativeEnabled {
			return nil, &agentproto.Error{Code: agentproto.ErrCodeForbidden, Message: "native workspaces are disabled on this host"}
		}
		if err := s.storage.Provision(ctx, wp); err != nil {
			return nil, err
		}
		return s.storage.Usage(ctx, wp.WorkspaceID)
	case agentproto.OpWorkspaceDestroy:
		return nil, s.destroyWorkspace(ctx, p.(*agentproto.WorkspaceRef))
	case agentproto.OpWorkspaceUsage:
		return s.storage.Usage(ctx, p.(*agentproto.WorkspaceRef).WorkspaceID)
	case agentproto.OpImageEnsure:
		return s.imageEnsure(ctx, p.(*agentproto.ImageEnsure))
	case agentproto.OpContainerCreate:
		return s.containerCreate(ctx, p.(*agentproto.ContainerCreate))
	case agentproto.OpContainerStart, agentproto.OpContainerStop, agentproto.OpContainerRestart,
		agentproto.OpContainerRemove, agentproto.OpContainerInspect:
		return s.containerAction(ctx, req.Operation, p.(*agentproto.ContainerRef))
	case agentproto.OpContainerLogs:
		cl := p.(*agentproto.ContainerLogs)
		id, err := s.resolveContainer(ctx, &cl.ContainerRef)
		if err != nil {
			return nil, err
		}
		lines, truncated, err := s.docker.Logs(ctx, id, cl.Tail, cl.Timestamps)
		if err != nil {
			return nil, err
		}
		return agentproto.LogsResult{Lines: lines, Truncated: truncated}, nil
	case agentproto.OpContainerStats:
		return s.containerStats(ctx, p.(*agentproto.ContainerStatsReq))
	case agentproto.OpContainerList:
		return s.containerList(ctx)
	case agentproto.OpFilesList, agentproto.OpFilesStat, agentproto.OpFilesMkdir:
		fp := p.(*agentproto.FilePath)
		return s.withFS(ctx, fp.WorkspaceID, func(f *fsops.FS) (any, error) {
			switch req.Operation {
			case agentproto.OpFilesList:
				entries, truncated, err := f.List(fp.Path)
				if err != nil {
					return nil, err
				}
				return map[string]any{"entries": entries, "truncated": truncated}, nil
			case agentproto.OpFilesStat:
				return f.Stat(fp.Path)
			default:
				return f.Mkdir(fp.Path)
			}
		})
	case agentproto.OpFilesRename:
		fr := p.(*agentproto.FileRename)
		return s.withFS(ctx, fr.WorkspaceID, func(f *fsops.FS) (any, error) { return f.Rename(fr.From, fr.To) })
	case agentproto.OpFilesDelete:
		fd := p.(*agentproto.FileDelete)
		return s.withFS(ctx, fd.WorkspaceID, func(f *fsops.FS) (any, error) { return nil, f.Delete(fd.Path, fd.Recursive) })
	case agentproto.OpFilesRead:
		fr := p.(*agentproto.FileRead)
		return s.withFS(ctx, fr.WorkspaceID, func(f *fsops.FS) (any, error) { return f.Read(fr.Path, fr.MaxBytes) })
	case agentproto.OpFilesWrite:
		fw := p.(*agentproto.FileWrite)
		return s.withFS(ctx, fw.WorkspaceID, func(f *fsops.FS) (any, error) { return f.Write(fw) })
	case agentproto.OpNativeUserEnsure:
		if !s.cfg.NativeEnabled {
			return nil, &agentproto.Error{Code: agentproto.ErrCodeForbidden, Message: "native workspaces are disabled on this host"}
		}
		nu := p.(*agentproto.NativeUserEnsure)
		home, _ := s.storage.DataDir(nu.WorkspaceID)
		return native.EnsureUser(ctx, nu.LinuxUser, home, s.cfg.NativeGroup, nu.WorkspaceID)
	case agentproto.OpBackupWorkspace:
		bw := p.(*agentproto.BackupWorkspace)
		for _, id := range bw.WorkspaceIDs {
			_ = s.storage.EnsureMounted(ctx, id)
		}
		return backup.Archive(s.cfg.BackupRoot, s.storage.DataDir, bw)
	case agentproto.OpRestoreWorkspace:
		rw := p.(*agentproto.RestoreWorkspace)
		n, err := backup.Restore(s.cfg.BackupRoot, s.storage.DataDir, rw)
		if err != nil {
			return nil, err
		}
		return map[string]int{"files_restored": n}, nil
	case agentproto.OpEmergencyStopAll:
		return s.emergencyStopAll(ctx)
	}
	return nil, invalid("operation not implemented")
}

func (s *Server) withFS(ctx context.Context, workspaceID string, fn func(*fsops.FS) (any, error)) (any, error) {
	if err := s.storage.EnsureMounted(ctx, workspaceID); err != nil {
		return nil, &agentproto.Error{Code: agentproto.ErrCodeNotFound, Message: "workspace storage is not available"}
	}
	dir, err := s.storage.DataDir(workspaceID)
	if err != nil {
		return nil, invalid(err.Error())
	}
	f, err := fsops.Open(dir)
	if err != nil {
		if errors.Is(err, fsops.ErrNotFound) {
			return nil, &agentproto.Error{Code: agentproto.ErrCodeNotFound, Message: "workspace storage is not provisioned"}
		}
		return nil, err
	}
	defer f.Close()
	return fn(f)
}

func (s *Server) agentInfo(ctx context.Context) (agentproto.AgentInfo, error) {
	info, err := hostinfo.Info(ctx)
	if err != nil {
		return info, err
	}
	info.AgentVersion = Version
	info.ProtocolVersion = agentproto.ProtocolVersion
	caps := agentproto.Capabilities{
		StorageDriver:    s.cfg.StorageDriver,
		StorageEnforced:  s.storage.Enforced(),
		NativeWorkspaces: s.cfg.NativeEnabled && native.Supported(),
		AppArmor:         hostinfo.AppArmorEnabled(),
	}
	dctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if v, err := s.docker.Version(dctx); err == nil {
		caps.Docker = true
		caps.DockerVersion = v.Version
		if di, err := s.docker.Info(dctx); err == nil {
			for _, o := range di.SecurityOptions {
				if strings.Contains(o, "name=seccomp") {
					caps.Seccomp = true
				}
			}
			if _, ok := di.Runtimes["nvidia"]; ok {
				caps.NVIDIARuntime = true
			}
		}
	} else {
		caps.DockerError = toAgentError(err).Message
	}
	if _, err := exec.LookPath(s.cfg.NvidiaSMI); err == nil {
		caps.NVIDIA = true
	}
	if _, err := exec.LookPath("nvidia-container-cli"); err == nil {
		caps.NVIDIARuntime = true
	}
	info.Capabilities = caps
	return info, nil
}

func (s *Server) hostMetrics(ctx context.Context) (agentproto.HostMetrics, error) {
	m, err := s.collector.Metrics(ctx)
	if err != nil {
		return m, err
	}
	dctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if v, err := s.docker.Version(dctx); err == nil {
		m.Docker.Available = true
		m.Docker.Version = v.Version
		if di, err := s.docker.Info(dctx); err == nil {
			m.Docker.ContainersRunning = di.ContainersRunning
			m.Docker.ContainersStopped = di.ContainersStopped
			m.Docker.ContainersPaused = di.ContainersPaused
			m.Docker.Images = di.Images
		}
	} else {
		m.Docker.Error = toAgentError(err).Message
	}
	return m, nil
}

func (s *Server) imageEnsure(ctx context.Context, ie *agentproto.ImageEnsure) (agentproto.ImageInfo, error) {
	ii, err := s.docker.InspectImage(ctx, ie.Reference)
	if err == nil {
		return agentproto.ImageInfo{Present: true, ID: ii.ID, SizeBytes: ii.Size}, nil
	}
	if !errors.Is(err, docker.ErrNotFound) {
		return agentproto.ImageInfo{}, err
	}
	if !ie.Pull {
		return agentproto.ImageInfo{Present: false}, nil
	}
	if err := s.docker.PullImage(ctx, ie.Reference); err != nil {
		return agentproto.ImageInfo{}, &agentproto.Error{Code: agentproto.ErrCodeNotFound,
			Message: "image is not available locally and could not be pulled: " + truncate(err.Error(), 200)}
	}
	ii, err = s.docker.InspectImage(ctx, ie.Reference)
	if err != nil {
		return agentproto.ImageInfo{}, err
	}
	return agentproto.ImageInfo{Present: true, ID: ii.ID, SizeBytes: ii.Size}, nil
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

func networkName(workspaceID string) string { return "itk-ws-" + workspaceID }

func (s *Server) containerCreate(ctx context.Context, c *agentproto.ContainerCreate) (agentproto.ContainerState, error) {
	// Idempotency: a container with this ITK id already exists.
	existing, err := s.docker.ListContainers(ctx, labelContainer+"="+c.ContainerID)
	if err != nil {
		return agentproto.ContainerState{}, err
	}
	if len(existing) > 0 {
		return s.inspectState(ctx, existing[0].ID)
	}
	if err := s.storage.EnsureMounted(ctx, c.WorkspaceID); err != nil {
		return agentproto.ContainerState{}, &agentproto.Error{Code: agentproto.ErrCodeConflict, Message: "workspace storage is not provisioned"}
	}
	data, _ := s.storage.DataDir(c.WorkspaceID)
	if _, err := os.Stat(data); err != nil {
		return agentproto.ContainerState{}, &agentproto.Error{Code: agentproto.ErrCodeConflict, Message: "workspace storage is not provisioned"}
	}
	if _, err := s.docker.InspectImage(ctx, c.Image); err != nil {
		if errors.Is(err, docker.ErrNotFound) {
			return agentproto.ContainerState{}, &agentproto.Error{Code: agentproto.ErrCodeNotFound, Message: "image " + c.Image + " is not available on the host"}
		}
		return agentproto.ContainerState{}, err
	}
	labels := map[string]string{labelManaged: "true", labelWorkspace: c.WorkspaceID, labelContainer: c.ContainerID, labelOwner: c.OwnerID}
	net := networkName(c.WorkspaceID)
	if _, err := s.docker.EnsureNetwork(ctx, net, map[string]string{labelManaged: "true", labelWorkspace: c.WorkspaceID}); err != nil {
		return agentproto.ContainerState{}, err
	}
	env := []string{"HOME=/home/itk", "USER=itk", "TERM=xterm-256color", "LANG=C.UTF-8",
		"ITK_WORKSPACE_ID=" + c.WorkspaceID, "ITK_CONTAINER_ID=" + c.ContainerID}
	for k, v := range c.Env {
		switch k {
		case "HOME", "USER", "ITK_WORKSPACE_ID", "ITK_CONTAINER_ID", "LD_PRELOAD":
			continue
		}
		env = append(env, k+"="+v)
	}
	cmd := c.Command
	if len(cmd) == 0 {
		cmd = []string{"sleep", "infinity"}
	}
	exposed := map[string]any{}
	bindings := map[string]any{}
	for _, p := range c.Ports {
		key := strconv.Itoa(p.Port) + "/tcp"
		exposed[key] = map[string]any{}
		// Published on loopback only: reachable through the authenticated
		// ITK Cloud proxy, never directly from the LAN.
		bindings[key] = []map[string]string{{"HostIp": "127.0.0.1", "HostPort": ""}}
	}
	secOpts := []string{"no-new-privileges:true"}
	if hostinfo.AppArmorEnabled() && s.cfg.AppArmorProfile != "" {
		secOpts = append(secOpts, "apparmor="+s.cfg.AppArmorProfile)
	}
	if s.cfg.SeccompProfile != "" {
		prof, err := os.ReadFile(s.cfg.SeccompProfile)
		if err != nil {
			return agentproto.ContainerState{}, fmt.Errorf("read seccomp profile: %w", err)
		}
		secOpts = append(secOpts, "seccomp="+string(prof))
	}
	tmpfs := map[string]string{"/tmp": "rw,nosuid,nodev,size=" + s.cfg.TmpfsSize}
	host := map[string]any{
		"NanoCpus":       int64(c.CPUMillis) * 1_000_000,
		"Memory":         c.MemoryBytes,
		"MemorySwap":     c.MemoryBytes, // no swap beyond the memory limit
		"PidsLimit":      c.PidsLimit,
		"CapDrop":        []string{"ALL"},
		"SecurityOpt":    secOpts,
		"Privileged":     false,
		"ReadonlyRootfs": c.ReadOnlyRoot,
		"Init":           true,
		"NetworkMode":    net,
		"PortBindings":   bindings,
		"RestartPolicy":  map[string]any{"Name": "no"},
		"Tmpfs":          tmpfs,
		"IpcMode":        "private",
		// Lets workspaces reach host services meant for them (project
		// databases) as host.docker.internal; the host firewall and pg_hba
		// restrict what is reachable.
		"ExtraHosts": []string{"host.docker.internal:host-gateway"},
		"Ulimits":    []map[string]any{{"Name": "nofile", "Soft": 8192, "Hard": 8192}},
		"LogConfig":  map[string]any{"Type": "json-file", "Config": map[string]string{"max-size": "10m", "max-file": "3"}},
		"Mounts": []map[string]any{{
			"Type": "bind", "Source": data, "Target": "/home/itk",
			"BindOptions": map[string]any{"Propagation": "rprivate"},
		}},
	}
	if s.cfg.RootfsSize != "" {
		host["StorageOpt"] = map[string]string{"size": s.cfg.RootfsSize}
	}
	if len(c.GPUDevices) > 0 {
		host["DeviceRequests"] = []map[string]any{{
			"Driver": "nvidia", "DeviceIDs": c.GPUDevices, "Capabilities": [][]string{{"gpu", "compute", "utility"}},
		}}
	}
	cfg := map[string]any{
		"Image":        c.Image,
		"Hostname":     c.Name,
		"User":         strconv.Itoa(c.RunAsUID) + ":" + strconv.Itoa(c.RunAsGID),
		"Env":          env,
		"Cmd":          cmd,
		"WorkingDir":   "/home/itk",
		"Labels":       labels,
		"ExposedPorts": exposed,
		"Tty":          false,
		"OpenStdin":    false,
		"HostConfig":   host,
	}
	id, err := s.docker.CreateContainer(ctx, "itk-"+c.ContainerID, cfg)
	if err != nil {
		var de *docker.APIError
		if errors.As(err, &de) && de.Status == 409 {
			// Name conflict from a concurrent create: return the existing one.
			if ex, lerr := s.docker.ListContainers(ctx, labelContainer+"="+c.ContainerID); lerr == nil && len(ex) > 0 {
				return s.inspectState(ctx, ex[0].ID)
			}
		}
		return agentproto.ContainerState{}, err
	}
	return s.inspectState(ctx, id)
}

// resolveContainer finds the Docker ID for a managed container and verifies
// it carries the expected ITK labels (the agent never touches unmanaged
// containers).
func (s *Server) resolveContainer(ctx context.Context, ref *agentproto.ContainerRef) (string, error) {
	if ref.RuntimeID != "" {
		cj, err := s.docker.InspectContainer(ctx, ref.RuntimeID)
		if err != nil {
			return "", err
		}
		if cj.Config.Labels[labelManaged] != "true" || cj.Config.Labels[labelContainer] != ref.ContainerID {
			return "", &agentproto.Error{Code: agentproto.ErrCodeForbidden, Message: "container is not managed by ITK Cloud"}
		}
		return cj.ID, nil
	}
	list, err := s.docker.ListContainers(ctx, labelContainer+"="+ref.ContainerID)
	if err != nil {
		return "", err
	}
	if len(list) == 0 || list[0].Labels[labelManaged] != "true" {
		return "", docker.ErrNotFound
	}
	return list[0].ID, nil
}

func (s *Server) containerAction(ctx context.Context, op string, ref *agentproto.ContainerRef) (any, error) {
	id, err := s.resolveContainer(ctx, ref)
	if op == agentproto.OpContainerRemove && errors.Is(err, docker.ErrNotFound) {
		return agentproto.ContainerState{Status: "missing"}, nil
	}
	if op == agentproto.OpContainerInspect && errors.Is(err, docker.ErrNotFound) {
		return agentproto.ContainerState{Status: "missing"}, nil
	}
	if err != nil {
		return nil, err
	}
	timeout := ref.TimeoutSec
	if timeout == 0 {
		timeout = 15
	}
	switch op {
	case agentproto.OpContainerStart:
		cj, err := s.docker.InspectContainer(ctx, id)
		if err != nil {
			return nil, err
		}
		if ws := cj.Config.Labels[labelWorkspace]; ws != "" {
			if err := s.storage.EnsureMounted(ctx, ws); err != nil {
				return nil, &agentproto.Error{Code: agentproto.ErrCodeConflict, Message: "workspace storage is not available"}
			}
		}
		err = s.docker.StartContainer(ctx, id)
	case agentproto.OpContainerStop:
		err = s.docker.StopContainer(ctx, id, timeout)
	case agentproto.OpContainerRestart:
		err = s.docker.RestartContainer(ctx, id, timeout)
	case agentproto.OpContainerRemove:
		if err := s.docker.RemoveContainer(ctx, id); err != nil {
			return nil, err
		}
		return agentproto.ContainerState{RuntimeID: id, Status: "removed"}, nil
	}
	if err != nil {
		return nil, err
	}
	return s.inspectState(ctx, id)
}

func parseDockerTime(s string) *time.Time {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil || t.Year() < 2000 {
		return nil
	}
	t = t.UTC()
	return &t
}

func (s *Server) inspectState(ctx context.Context, id string) (agentproto.ContainerState, error) {
	cj, err := s.docker.InspectContainer(ctx, id)
	if err != nil {
		if errors.Is(err, docker.ErrNotFound) {
			return agentproto.ContainerState{Status: "missing"}, nil
		}
		return agentproto.ContainerState{}, err
	}
	st := agentproto.ContainerState{
		RuntimeID: cj.ID, Status: cj.State.Status, ExitCode: cj.State.ExitCode, Error: cj.State.Error,
		OOMKilled: cj.State.OOMKilled, StartedAt: parseDockerTime(cj.State.StartedAt),
		FinishedAt: parseDockerTime(cj.State.FinishedAt), HostPorts: map[int]int{}, Labels: cj.Config.Labels,
	}
	for k, binds := range cj.NetworkSettings.Ports {
		port, _ := strconv.Atoi(strings.TrimSuffix(k, "/tcp"))
		for _, b := range binds {
			if hp, err := strconv.Atoi(b.HostPort); err == nil && (b.HostIP == "127.0.0.1" || b.HostIP == "") {
				st.HostPorts[port] = hp
			}
		}
	}
	return st, nil
}

func (s *Server) containerStats(ctx context.Context, req *agentproto.ContainerStatsReq) ([]agentproto.ContainerStats, error) {
	out := make([]agentproto.ContainerStats, 0, len(req.RuntimeIDs))
	for _, id := range req.RuntimeIDs {
		cj, err := s.docker.InspectContainer(ctx, id)
		if err != nil || cj.Config.Labels[labelManaged] != "true" || !cj.State.Running {
			continue
		}
		st, err := s.docker.Stats(ctx, id)
		if err != nil {
			continue
		}
		cs := agentproto.ContainerStats{RuntimeID: cj.ID, MemoryLimit: int64(st.MemoryStats.Limit), Pids: int64(st.PidsStats.Current)}
		mem := st.MemoryStats.Usage
		if inactive, ok := st.MemoryStats.Stats["inactive_file"]; ok && inactive < mem {
			mem -= inactive // matches `docker stats` (cgroup v2)
		}
		cs.MemoryBytes = int64(mem)
		cpuDelta := float64(st.CPUStats.CPUUsage.TotalUsage) - float64(st.PreCPUStats.CPUUsage.TotalUsage)
		sysDelta := float64(st.CPUStats.SystemUsage) - float64(st.PreCPUStats.SystemUsage)
		if sysDelta > 0 && cpuDelta > 0 {
			online := float64(st.CPUStats.OnlineCPUs)
			if online == 0 {
				online = 1
			}
			cs.CPUPercent = cpuDelta / sysDelta * online * 100
		}
		for _, n := range st.Networks {
			cs.NetRxBytes += int64(n.RxBytes)
			cs.NetTxBytes += int64(n.TxBytes)
		}
		out = append(out, cs)
	}
	return out, nil
}

func (s *Server) containerList(ctx context.Context) ([]agentproto.ManagedContainer, error) {
	list, err := s.docker.ListContainers(ctx, labelManaged+"=true")
	if err != nil {
		return nil, err
	}
	out := make([]agentproto.ManagedContainer, 0, len(list))
	for _, c := range list {
		out = append(out, agentproto.ManagedContainer{RuntimeID: c.ID, ContainerID: c.Labels[labelContainer],
			WorkspaceID: c.Labels[labelWorkspace], State: c.State})
	}
	return out, nil
}

func (s *Server) destroyWorkspace(ctx context.Context, ref *agentproto.WorkspaceRef) error {
	// Remove any remaining containers of the workspace first.
	if list, err := s.docker.ListContainers(ctx, labelWorkspace+"="+ref.WorkspaceID); err == nil {
		for _, c := range list {
			if c.Labels[labelManaged] == "true" {
				if err := s.docker.RemoveContainer(ctx, c.ID); err != nil {
					return err
				}
			}
		}
		if err := s.docker.RemoveNetwork(ctx, networkName(ref.WorkspaceID)); err != nil {
			s.log.Warn("remove workspace network", "workspace_id", ref.WorkspaceID, "error", err)
		}
	} else if !errors.Is(err, docker.ErrUnavailable) {
		return err
	}
	if err := s.storage.Destroy(ctx, ref.WorkspaceID); err != nil {
		return err
	}
	if ref.Native && ref.LinuxUser != "" {
		return native.DeleteUser(ctx, ref.LinuxUser)
	}
	return nil
}

func (s *Server) emergencyStopAll(ctx context.Context) (map[string]any, error) {
	list, err := s.docker.ListContainers(ctx, labelManaged+"=true")
	if err != nil {
		return nil, err
	}
	stopped, failed := 0, 0
	for _, c := range list {
		if c.State != "running" {
			continue
		}
		if err := s.docker.StopContainer(ctx, c.ID, 10); err != nil {
			failed++
			continue
		}
		stopped++
	}
	// Native workspace shells run in the itk-native.slice systemd slice.
	nativeStopped := false
	if s.cfg.NativeEnabled && native.Supported() {
		cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		cmd := exec.CommandContext(cctx, "systemctl", "stop", "itk-native.slice")
		cmd.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin"}
		nativeStopped = cmd.Run() == nil
	}
	return map[string]any{"stopped": stopped, "failed": failed, "native_sessions_stopped": nativeStopped}, nil
}

// attributeGPUProcesses maps GPU compute processes to ITK Cloud containers
// using the process cgroup and the managed-container labels.
func (s *Server) attributeGPUProcesses(ctx context.Context, inv *agentproto.GPUInventory) {
	hasProcs := false
	for _, g := range inv.GPUs {
		if len(g.Processes) > 0 {
			hasProcs = true
			break
		}
	}
	if !hasProcs {
		return
	}
	list, err := s.docker.ListContainers(ctx, labelManaged+"=true")
	if err != nil {
		return
	}
	byRuntime := map[string]string{}
	for _, c := range list {
		byRuntime[c.ID] = c.Labels[labelContainer]
	}
	for gi := range inv.GPUs {
		for pi := range inv.GPUs[gi].Processes {
			p := &inv.GPUs[gi].Processes[pi]
			if rid := gpu.ContainerOfPID(p.PID); rid != "" {
				p.ContainerID = byRuntime[rid]
			}
		}
	}
}
