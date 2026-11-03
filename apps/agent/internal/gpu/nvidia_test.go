package gpu

import "testing"

func TestParseQuery(t *testing.T) {
	out := `0, GPU-11111111-2222-3333-4444-555555555555, NVIDIA GeForce RTX 4090, 550.54.14, 24564, 1024, 37, 45, 63.21, [N/A]
1, GPU-aaaaaaaa-2222-3333-4444-555555555555, NVIDIA A100-SXM4-40GB, 550.54.14, 40960, [N/A], [N/A], 33, [N/A], Disabled
`
	gpus, err := ParseQuery(out)
	if err != nil || len(gpus) != 2 {
		t.Fatalf("parse: %v %d", err, len(gpus))
	}
	g := gpus[0]
	if g.Name != "NVIDIA GeForce RTX 4090" || *g.MemoryTotal != 24564*1024*1024 || *g.Utilization != 37 || g.MIGMode != "" {
		t.Fatalf("unexpected gpu0: %+v", g)
	}
	if gpus[1].MemoryUsed != nil || gpus[1].Utilization != nil || gpus[1].MIGMode != "Disabled" {
		t.Fatalf("N/A values must be nil: %+v", gpus[1])
	}
}

func TestParseProcesses(t *testing.T) {
	out := "GPU-11111111-2222-3333-4444-555555555555, 4242, python, 2048\n"
	p := ParseProcesses(out)["GPU-11111111-2222-3333-4444-555555555555"]
	if len(p) != 1 || p[0].PID != 4242 || p[0].MemoryBytes != 2048*1024*1024 {
		t.Fatalf("unexpected: %+v", p)
	}
}

func TestContainerIDFromCgroup(t *testing.T) {
	id := "4f1c2a5e9b7d3c8a1e6f0b2d4c6a8e0f1a3c5e7b9d1f3a5c7e9b1d3f5a7c9e1b"
	for _, s := range []string{"0::/system.slice/docker-" + id + ".scope\n", "12:memory:/docker/" + id + "\n"} {
		if got := ContainerIDFromCgroup(s); got != id {
			t.Fatalf("got %q from %q", got, s)
		}
	}
	if ContainerIDFromCgroup("0::/user.slice/user-1000.slice\n") != "" {
		t.Fatal("non-container process must not be attributed")
	}
}
