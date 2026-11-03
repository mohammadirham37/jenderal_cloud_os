package agentproto

import "testing"

func TestPlatformUpdateValidate(t *testing.T) {
	ok := []PlatformUpdate{
		{Version: "0.1.6", Repo: "mohammadirham37/jenderal_cloud_os", Arch: "amd64"},
		{Version: "1.0.0-rc.1", Repo: "a/b.c", Arch: "arm64"},
	}
	for _, p := range ok {
		if err := p.Validate(); err != nil {
			t.Errorf("%+v: %v", p, err)
		}
	}
	bad := []PlatformUpdate{
		{Version: "v0.1.6", Repo: "a/b", Arch: "amd64"},
		{Version: "0.1", Repo: "a/b", Arch: "amd64"},
		{Version: "0.1.6;rm -rf /", Repo: "a/b", Arch: "amd64"},
		{Version: "01.1.6", Repo: "a/b", Arch: "amd64"},
		{Version: "0.1.6", Repo: "a/b/c", Arch: "amd64"},
		{Version: "0.1.6", Repo: "a/..", Arch: "amd64"},
		{Version: "0.1.6", Repo: "../b", Arch: "amd64"},
		{Version: "0.1.6", Repo: "https://evil/x", Arch: "amd64"},
		{Version: "0.1.6", Repo: "a/b", Arch: "386"},
	}
	for _, p := range bad {
		if err := p.Validate(); err == nil {
			t.Errorf("%+v must be rejected", p)
		}
	}
	if _, ok := Operations[OpPlatformUpdate]; !ok {
		t.Error("platform.update must be in the allow-list")
	}
}
