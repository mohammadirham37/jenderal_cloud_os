package fsops

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/itk-cloud/itk-cloud/packages/agentproto"
)

func setup(t *testing.T) (*FS, string, string) {
	t.Helper()
	base := t.TempDir()
	data := filepath.Join(base, "data")
	if err := os.Mkdir(data, 0o755); err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(base, "secret.txt")
	if err := os.WriteFile(secret, []byte("host secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := Open(data)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.Close() })
	return f, data, secret
}

func TestCRUD(t *testing.T) {
	f, _, _ := setup(t)
	if _, err := f.Mkdir("src"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write(&agentproto.FileWrite{Path: "src/main.go", Content: "package main\n", Create: true}); err != nil {
		t.Fatal(err)
	}
	list, _, err := f.List("")
	if err != nil || len(list) != 1 || list[0].Type != "dir" {
		t.Fatalf("list root: %+v %v", list, err)
	}
	c, err := f.Read("src/main.go", 1024)
	if err != nil || c.Content != "package main\n" || c.Binary {
		t.Fatalf("read: %+v %v", c, err)
	}
	if _, err := f.Rename("src/main.go", "src/app.go"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Rename("src/app.go", "src"); !errors.Is(err, ErrExists) {
		t.Fatalf("rename onto existing must fail, got %v", err)
	}
	if err := f.Delete("src", false); !errors.Is(err, ErrNotEmpty) {
		t.Fatalf("non-recursive delete of non-empty dir must fail, got %v", err)
	}
	if err := f.Delete("src", true); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Stat("src"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected not found, got %v", err)
	}
}

func TestSymlinkEscapeIsBlocked(t *testing.T) {
	f, data, secret := setup(t)
	if err := os.Symlink(secret, filepath.Join(data, "escape")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../", filepath.Join(data, "up")); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Read("escape", 1024); err == nil {
		t.Fatal("reading through an escaping symlink must fail")
	}
	if _, err := f.Read("up/secret.txt", 1024); err == nil {
		t.Fatal("reading through a parent symlink must fail")
	}
	if _, err := f.Write(&agentproto.FileWrite{Path: "up/pwned.txt", Content: "x", Create: true}); err == nil {
		t.Fatal("writing through an escaping symlink must fail")
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(data), "pwned.txt")); err == nil {
		t.Fatal("file was created outside the workspace")
	}
}

func TestUploadLimitAndBinaryDetection(t *testing.T) {
	f, _, _ := setup(t)
	if _, err := f.Upload("big.bin", strings.NewReader(strings.Repeat("a", 100)), 10, false); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("expected too large, got %v", err)
	}
	if _, err := f.Upload("bin.dat", strings.NewReader("a\x00b"), 10, false); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Upload("bin.dat", strings.NewReader("x"), 10, false); !errors.Is(err, ErrExists) {
		t.Fatalf("upload without overwrite must not replace, got %v", err)
	}
	c, err := f.Read("bin.dat", 100)
	if err != nil || !c.Binary {
		t.Fatalf("binary not detected: %+v %v", c, err)
	}
	if _, err := f.Read("bin.dat", 1); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("expected too large, got %v", err)
	}
	entries, _, _ := f.List("")
	for _, e := range entries {
		if strings.HasPrefix(e.Name, ".itk-tmp-") {
			t.Fatal("temporary upload file left behind")
		}
	}
}

func TestWriteConflictDetection(t *testing.T) {
	f, _, _ := setup(t)
	e, err := f.Write(&agentproto.FileWrite{Path: "a.txt", Content: "1", Create: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write(&agentproto.FileWrite{Path: "a.txt", Content: "2", ExpectedModTime: "2000-01-01T00:00:00Z"}); !errors.Is(err, ErrModified) {
		t.Fatalf("expected conflict, got %v", err)
	}
	if _, err := f.Write(&agentproto.FileWrite{Path: "a.txt", Content: "2", ExpectedModTime: e.ModTime.Format("2006-01-02T15:04:05.999999999Z07:00")}); err != nil {
		t.Fatalf("matching mod time must succeed: %v", err)
	}
}
