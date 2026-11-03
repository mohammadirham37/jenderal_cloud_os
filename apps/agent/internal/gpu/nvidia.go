// Package gpu discovers NVIDIA GPUs using nvidia-smi with fixed query
// arguments. Metrics the driver reports as "[N/A]" are returned as nil.
package gpu

import (
	"context"
	"encoding/csv"
	"errors"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/itk-cloud/itk-cloud/packages/agentproto"
)

// Inventory queries nvidia-smi.
func Inventory(ctx context.Context, smi string) agentproto.GPUInventory {
	if _, err := exec.LookPath(smi); err != nil {
		return agentproto.GPUInventory{Available: false, Error: "nvidia-smi not found (NVIDIA driver not installed)", GPUs: []agentproto.GPUDevice{}}
	}
	out, err := run(ctx, smi, "--query-gpu=index,uuid,name,driver_version,memory.total,memory.used,utilization.gpu,temperature.gpu,power.draw,mig.mode.current",
		"--format=csv,noheader,nounits")
	if err != nil {
		return agentproto.GPUInventory{Available: false, Error: err.Error(), GPUs: []agentproto.GPUDevice{}}
	}
	gpus, err := ParseQuery(out)
	if err != nil {
		return agentproto.GPUInventory{Available: false, Error: err.Error(), GPUs: []agentproto.GPUDevice{}}
	}
	cuda := cudaVersion(ctx, smi)
	byUUID := map[string]int{}
	for i := range gpus {
		gpus[i].CUDAVersion = cuda
		byUUID[gpus[i].UUID] = i
	}
	if pout, err := run(ctx, smi, "--query-compute-apps=gpu_uuid,pid,process_name,used_memory", "--format=csv,noheader,nounits"); err == nil {
		for uuid, procs := range ParseProcesses(pout) {
			if i, ok := byUUID[uuid]; ok {
				gpus[i].Processes = procs
			}
		}
	}
	return agentproto.GPUInventory{Available: len(gpus) > 0, GPUs: gpus}
}

func run(ctx context.Context, name string, args ...string) (string, error) {
	cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(cctx, name, args...)
	cmd.Env = []string{"PATH=/usr/bin:/bin:/usr/sbin:/sbin", "LC_ALL=C"}
	b, err := cmd.Output()
	if err != nil {
		return "", errors.New("nvidia-smi failed: " + err.Error())
	}
	return string(b), nil
}

func cudaVersion(ctx context.Context, smi string) string {
	out, err := run(ctx, smi)
	if err != nil {
		return ""
	}
	if i := strings.Index(out, "CUDA Version:"); i >= 0 {
		f := strings.Fields(out[i+len("CUDA Version:"):])
		if len(f) > 0 {
			return f[0]
		}
	}
	return ""
}

func na(s string) bool {
	s = strings.TrimSpace(s)
	return s == "" || strings.Contains(s, "N/A") || strings.Contains(s, "Not Supported")
}

func optInt(s string) *int {
	if na(s) {
		return nil
	}
	f, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil {
		return nil
	}
	n := int(f)
	return &n
}

func optMiB(s string) *int64 {
	if na(s) {
		return nil
	}
	f, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil {
		return nil
	}
	n := int64(f * 1024 * 1024)
	return &n
}

func optFloat(s string) *float64 {
	if na(s) {
		return nil
	}
	f, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil {
		return nil
	}
	return &f
}

// ParseQuery parses --query-gpu CSV output.
func ParseQuery(out string) ([]agentproto.GPUDevice, error) {
	r := csv.NewReader(strings.NewReader(strings.TrimSpace(out)))
	r.TrimLeadingSpace = true
	r.FieldsPerRecord = -1
	recs, err := r.ReadAll()
	if err != nil {
		return nil, err
	}
	gpus := []agentproto.GPUDevice{}
	for _, f := range recs {
		if len(f) < 10 {
			continue
		}
		idx, err := strconv.Atoi(strings.TrimSpace(f[0]))
		if err != nil {
			continue
		}
		mig := strings.TrimSpace(f[9])
		if na(mig) {
			mig = ""
		}
		gpus = append(gpus, agentproto.GPUDevice{
			Index: idx, UUID: strings.TrimSpace(f[1]), Name: strings.TrimSpace(f[2]), DriverVersion: strings.TrimSpace(f[3]),
			MemoryTotal: optMiB(f[4]), MemoryUsed: optMiB(f[5]), Utilization: optInt(f[6]), Temperature: optInt(f[7]),
			PowerDraw: optFloat(f[8]), MIGMode: mig, Processes: []agentproto.GPUProcess{},
		})
	}
	return gpus, nil
}

// ParseProcesses parses --query-compute-apps output keyed by GPU UUID.
func ParseProcesses(out string) map[string][]agentproto.GPUProcess {
	res := map[string][]agentproto.GPUProcess{}
	r := csv.NewReader(strings.NewReader(strings.TrimSpace(out)))
	r.TrimLeadingSpace = true
	r.FieldsPerRecord = -1
	recs, err := r.ReadAll()
	if err != nil {
		return res
	}
	for _, f := range recs {
		if len(f) < 4 {
			continue
		}
		pid, err := strconv.Atoi(strings.TrimSpace(f[1]))
		if err != nil {
			continue
		}
		p := agentproto.GPUProcess{PID: pid, Name: strings.TrimSpace(f[2])}
		if m := optMiB(f[3]); m != nil {
			p.MemoryBytes = *m
		}
		u := strings.TrimSpace(f[0])
		res[u] = append(res[u], p)
	}
	return res
}
