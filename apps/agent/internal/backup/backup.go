// Package backup archives and restores workspace files. Archives are
// tar+gzip streams encrypted with age to an X25519 recipient; the agent never
// writes plaintext archives to disk.
package backup

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"syscall"

	"filippo.io/age"

	"github.com/itk-cloud/itk-cloud/packages/agentproto"
)

// DataDirFunc resolves a workspace data directory.
type DataDirFunc func(workspaceID string) (string, error)

// Archive writes an encrypted archive of the given workspaces.
func Archive(backupRoot string, dataDir DataDirFunc, req *agentproto.BackupWorkspace) (agentproto.BackupResult, error) {
	rcpt, err := age.ParseX25519Recipient(req.AgeRecipient)
	if err != nil {
		return agentproto.BackupResult{}, fmt.Errorf("invalid age recipient: %w", err)
	}
	if err := os.MkdirAll(backupRoot, 0o700); err != nil {
		return agentproto.BackupResult{}, err
	}
	dst := filepath.Join(backupRoot, req.FileName)
	tmp := dst + ".partial"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return agentproto.BackupResult{}, err
	}
	ok := false
	defer func() {
		if !ok {
			f.Close()
			os.Remove(tmp)
		}
	}()
	h := sha256.New()
	counter := &countWriter{w: io.MultiWriter(f, h)}
	enc, err := age.Encrypt(counter, rcpt)
	if err != nil {
		return agentproto.BackupResult{}, err
	}
	gz := gzip.NewWriter(enc)
	tw := tar.NewWriter(gz)
	for _, id := range req.WorkspaceIDs {
		dir, err := dataDir(id)
		if err != nil {
			return agentproto.BackupResult{}, err
		}
		if _, err := os.Stat(dir); errors.Is(err, fs.ErrNotExist) {
			continue // workspace without storage (e.g. failed provisioning)
		}
		if err := addTree(tw, dir, id); err != nil {
			return agentproto.BackupResult{}, fmt.Errorf("archive %s: %w", id, err)
		}
	}
	for _, c := range []io.Closer{tw, gz, enc} {
		if err := c.Close(); err != nil {
			return agentproto.BackupResult{}, err
		}
	}
	if err := f.Sync(); err != nil {
		return agentproto.BackupResult{}, err
	}
	if err := f.Close(); err != nil {
		return agentproto.BackupResult{}, err
	}
	if err := os.Rename(tmp, dst); err != nil {
		return agentproto.BackupResult{}, err
	}
	ok = true
	return agentproto.BackupResult{FileName: req.FileName, Size: counter.n, SHA256: hex.EncodeToString(h.Sum(nil))}, nil
}

type countWriter struct {
	w io.Writer
	n int64
}

func (c *countWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	return n, err
}

func addTree(tw *tar.Writer, root, prefix string) error {
	return filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // skip unreadable entries rather than failing the backup
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		if rel == "lost+found" && d.IsDir() {
			return filepath.SkipDir
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		link := ""
		if info.Mode()&fs.ModeSymlink != 0 {
			if link, err = os.Readlink(p); err != nil {
				return nil
			}
		} else if !info.Mode().IsRegular() && !info.IsDir() {
			return nil // skip sockets, FIFOs, devices
		}
		hdr, err := tar.FileInfoHeader(info, link)
		if err != nil {
			return err
		}
		hdr.Name = path.Join(prefix, filepath.ToSlash(rel))
		if info.IsDir() {
			hdr.Name += "/"
		}
		hdr.Uname, hdr.Gname = "", ""
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		if info.Mode().IsRegular() {
			f, err := os.Open(p)
			if err != nil {
				return nil
			}
			_, err = io.CopyN(tw, f, hdr.Size)
			f.Close()
			return err
		}
		return nil
	})
}

// Restore decrypts an archive and extracts it into the workspace data
// directories through os.Root (no path can escape a workspace). Existing
// files are overwritten; files not present in the archive are kept.
func Restore(backupRoot string, dataDir DataDirFunc, req *agentproto.RestoreWorkspace) (int, error) {
	ident, err := age.ParseX25519Identity(strings.TrimSpace(req.AgeIdentity))
	if err != nil {
		return 0, errors.New("invalid age identity")
	}
	f, err := os.Open(filepath.Join(backupRoot, req.FileName))
	if err != nil {
		return 0, err
	}
	defer f.Close()
	dec, err := age.Decrypt(f, ident)
	if err != nil {
		return 0, fmt.Errorf("decrypt: %w", err)
	}
	gz, err := gzip.NewReader(dec)
	if err != nil {
		return 0, err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	roots := map[string]*os.Root{}
	owners := map[string][2]int{}
	defer func() {
		for _, r := range roots {
			r.Close()
		}
	}()
	restored := 0
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return restored, nil
		}
		if err != nil {
			return restored, err
		}
		id, rel, ok := strings.Cut(strings.TrimSuffix(hdr.Name, "/"), "/")
		if !agentproto.IsUUID(id) {
			return restored, fmt.Errorf("unexpected archive entry %q", hdr.Name)
		}
		if req.WorkspaceID != "" && id != req.WorkspaceID {
			continue
		}
		if !ok || rel == "" {
			continue
		}
		clean, err := agentproto.CleanRelPath(rel)
		if err != nil || clean == "" {
			return restored, fmt.Errorf("unsafe archive entry %q", hdr.Name)
		}
		r, found := roots[id]
		if !found {
			dir, err := dataDir(id)
			if err != nil {
				return restored, err
			}
			st, err := os.Stat(dir)
			if err != nil {
				return restored, fmt.Errorf("workspace %s storage is not provisioned; provision it before restoring", id)
			}
			if r, err = os.OpenRoot(dir); err != nil {
				return restored, err
			}
			roots[id] = r
			uid, gid := -1, -1
			if sys, ok := st.Sys().(*syscall.Stat_t); ok {
				uid, gid = int(sys.Uid), int(sys.Gid)
			}
			owners[id] = [2]int{uid, gid}
		}
		own := owners[id]
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := r.MkdirAll(clean, 0o755); err != nil {
				return restored, err
			}
		case tar.TypeReg:
			if dir := path.Dir(clean); dir != "." {
				if err := r.MkdirAll(dir, 0o755); err != nil {
					return restored, err
				}
			}
			_ = r.Remove(clean)
			out, err := r.OpenFile(clean, os.O_CREATE|os.O_EXCL|os.O_WRONLY, fs.FileMode(hdr.Mode).Perm())
			if err != nil {
				return restored, err
			}
			_, cerr := io.CopyN(out, tr, hdr.Size)
			out.Close()
			if cerr != nil {
				return restored, cerr
			}
			restored++
		case tar.TypeSymlink:
			_ = r.Remove(clean)
			if err := r.Symlink(hdr.Linkname, clean); err != nil {
				return restored, err
			}
		default:
			continue
		}
		if own[0] >= 0 {
			_ = r.Lchown(clean, own[0], own[1])
		}
	}
}
