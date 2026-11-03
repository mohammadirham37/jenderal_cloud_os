package backups

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"filippo.io/age"

	"github.com/itk-cloud/itk-cloud/internal/platform/config"
)

func TestConfigArchiveSkipsUnreadableFilesAndIsEncrypted(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can read every file")
	}
	cfgDir, out := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(cfgDir, "itk-cloud.env"), []byte("APP_ENV=production\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, "agent.env"), []byte("SECRET=x\n"), 0o000); err != nil {
		t.Fatal(err)
	}
	id, _ := age.GenerateX25519Identity()
	s := &Service{Cfg: &config.Config{ConfigDir: cfgDir, BackupRoot: out, BackupAgeRecipient: id.Recipient().String()}}
	res, err := s.archiveConfig(context.Background(), "config.tar.gz.age")
	if err != nil {
		t.Fatalf("archive: %v", err)
	}
	f, err := os.Open(filepath.Join(out, res.FileName))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	r, err := age.Decrypt(f, id)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	gz, err := gzip.NewReader(r)
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(gz)
	names := map[string]bool{}
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("archive is corrupt: %v", err)
		}
		names[h.Name] = true
	}
	if !names["config/itk-cloud.env"] || !names["config/SKIPPED-UNREADABLE.txt"] || names["config/agent.env"] {
		t.Fatalf("unexpected archive contents: %v", names)
	}
}
