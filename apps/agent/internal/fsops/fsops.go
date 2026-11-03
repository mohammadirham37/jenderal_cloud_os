// Package fsops performs file operations confined to a workspace data
// directory. All access goes through os.Root, which resolves paths (including
// symlinks) without ever escaping the root directory. Files created by the
// agent are chowned to the owner of the workspace root.
package fsops

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"os"
	"path"
	"sort"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/itk-cloud/itk-cloud/packages/agentproto"
)

// MaxListEntries bounds directory listings.
const MaxListEntries = 5000

// Errors mapped to agent error codes by the server.
var (
	ErrNotFound = errors.New("not found")
	ErrExists   = errors.New("already exists")
	ErrNotDir   = errors.New("not a directory")
	ErrNotFile  = errors.New("not a regular file")
	ErrTooLarge = errors.New("file too large")
	ErrNotEmpty = errors.New("directory not empty")
	ErrModified = errors.New("file was modified by someone else")
)

// FS is an open workspace root.
type FS struct {
	root     *os.Root
	uid, gid int
}

// Open opens a workspace data directory.
func Open(dataDir string) (*FS, error) {
	info, err := os.Stat(dataDir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	r, err := os.OpenRoot(dataDir)
	if err != nil {
		return nil, err
	}
	uid, gid := ownerOf(info)
	return &FS{root: r, uid: uid, gid: gid}, nil
}

// Close releases the root.
func (f *FS) Close() error { return f.root.Close() }

func name(p string) string {
	if p == "" {
		return "."
	}
	return p
}

func mapErr(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, fs.ErrNotExist):
		return ErrNotFound
	case errors.Is(err, syscall.ENOTEMPTY):
		return ErrNotEmpty // must precede ErrExist: ENOTEMPTY also matches fs.ErrExist
	case errors.Is(err, fs.ErrExist):
		return ErrExists
	case errors.Is(err, syscall.ENOTDIR):
		return ErrNotDir
	}
	return err
}

func entry(p string, info fs.FileInfo) agentproto.FileEntry {
	t := "other"
	switch {
	case info.Mode().IsRegular():
		t = "file"
	case info.IsDir():
		t = "dir"
	case info.Mode()&fs.ModeSymlink != 0:
		t = "symlink"
	}
	n := info.Name()
	if p == "" {
		n = ""
	}
	return agentproto.FileEntry{Name: n, Path: p, Type: t, Size: info.Size(), Mode: info.Mode().Perm().String(), ModTime: info.ModTime().UTC()}
}

// List lists a directory (directories first, then by name).
func (f *FS) List(p string) ([]agentproto.FileEntry, bool, error) {
	info, err := f.root.Stat(name(p))
	if err != nil {
		return nil, false, mapErr(err)
	}
	if !info.IsDir() {
		return nil, false, ErrNotDir
	}
	d, err := f.root.Open(name(p))
	if err != nil {
		return nil, false, mapErr(err)
	}
	defer d.Close()
	des, err := d.ReadDir(MaxListEntries + 1)
	if err != nil && err != io.EOF {
		return nil, false, err
	}
	truncated := len(des) > MaxListEntries
	if truncated {
		des = des[:MaxListEntries]
	}
	out := make([]agentproto.FileEntry, 0, len(des))
	for _, de := range des {
		info, err := de.Info()
		if err != nil {
			continue
		}
		out = append(out, entry(path.Join(p, de.Name()), info))
	}
	sort.Slice(out, func(i, j int) bool {
		if (out[i].Type == "dir") != (out[j].Type == "dir") {
			return out[i].Type == "dir"
		}
		return out[i].Name < out[j].Name
	})
	return out, truncated, nil
}

// Stat describes a path without following a final symlink.
func (f *FS) Stat(p string) (agentproto.FileEntry, error) {
	info, err := f.root.Lstat(name(p))
	if err != nil {
		return agentproto.FileEntry{}, mapErr(err)
	}
	return entry(p, info), nil
}

// Mkdir creates a directory (parents must exist).
func (f *FS) Mkdir(p string) (agentproto.FileEntry, error) {
	if p == "" {
		return agentproto.FileEntry{}, ErrExists
	}
	if err := f.root.Mkdir(p, 0o755); err != nil {
		return agentproto.FileEntry{}, mapErr(err)
	}
	if err := f.root.Lchown(p, f.uid, f.gid); err != nil {
		return agentproto.FileEntry{}, err
	}
	return f.Stat(p)
}

// Rename moves a path; the destination must not exist.
func (f *FS) Rename(from, to string) (agentproto.FileEntry, error) {
	if _, err := f.root.Lstat(from); err != nil {
		return agentproto.FileEntry{}, mapErr(err)
	}
	if _, err := f.root.Lstat(to); err == nil {
		return agentproto.FileEntry{}, ErrExists
	}
	if err := f.root.Rename(from, to); err != nil {
		return agentproto.FileEntry{}, mapErr(err)
	}
	return f.Stat(to)
}

// Delete removes a file, symlink or directory.
func (f *FS) Delete(p string, recursive bool) error {
	info, err := f.root.Lstat(p)
	if err != nil {
		return mapErr(err)
	}
	if info.IsDir() && recursive {
		return mapErr(f.root.RemoveAll(p))
	}
	return mapErr(f.root.Remove(p))
}

// openRegular opens a path for reading and verifies it is a regular file
// (O_NONBLOCK prevents hanging on FIFOs swapped in after a check).
func (f *FS) openRegular(p string) (*os.File, fs.FileInfo, error) {
	fh, err := f.root.OpenFile(p, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, nil, mapErr(err)
	}
	info, err := fh.Stat()
	if err != nil {
		fh.Close()
		return nil, nil, err
	}
	if !info.Mode().IsRegular() {
		fh.Close()
		return nil, nil, ErrNotFile
	}
	return fh, info, nil
}

// Read returns the content of a text file up to max bytes.
func (f *FS) Read(p string, max int64) (agentproto.FileContent, error) {
	fh, info, err := f.openRegular(p)
	if err != nil {
		return agentproto.FileContent{}, err
	}
	defer fh.Close()
	if info.Size() > max {
		return agentproto.FileContent{}, ErrTooLarge
	}
	b, err := io.ReadAll(io.LimitReader(fh, max+1))
	if err != nil {
		return agentproto.FileContent{}, err
	}
	if int64(len(b)) > max {
		return agentproto.FileContent{}, ErrTooLarge
	}
	fc := agentproto.FileContent{Size: info.Size(), ModTime: info.ModTime().UTC()}
	sniff := b
	if len(sniff) > 8192 {
		sniff = sniff[:8192]
	}
	if bytes.IndexByte(sniff, 0) >= 0 || !utf8.Valid(b) {
		fc.Binary = true
		return fc, nil
	}
	fc.Content = string(b)
	return fc, nil
}

func tmpName(dir string) string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return path.Join(dir, ".itk-tmp-"+hex.EncodeToString(b))
}

// Write atomically replaces (or creates) a file with content.
func (f *FS) Write(w *agentproto.FileWrite) (agentproto.FileEntry, error) {
	info, err := f.root.Lstat(w.Path)
	switch {
	case err == nil:
		if !info.Mode().IsRegular() {
			return agentproto.FileEntry{}, ErrNotFile
		}
		if w.ExpectedModTime != "" {
			exp, perr := time.Parse(time.RFC3339Nano, w.ExpectedModTime)
			if perr == nil && !info.ModTime().UTC().Equal(exp.UTC()) {
				return agentproto.FileEntry{}, ErrModified
			}
		}
	case errors.Is(err, fs.ErrNotExist):
		if !w.Create {
			return agentproto.FileEntry{}, ErrNotFound
		}
	default:
		return agentproto.FileEntry{}, mapErr(err)
	}
	return f.writeStream(w.Path, bytes.NewReader([]byte(w.Content)), int64(len(w.Content))+1, true)
}

// Upload streams r into path (max bytes enforced).
func (f *FS) Upload(p string, r io.Reader, max int64, overwrite bool) (agentproto.FileEntry, error) {
	if info, err := f.root.Lstat(p); err == nil {
		if !overwrite {
			return agentproto.FileEntry{}, ErrExists
		}
		if !info.Mode().IsRegular() {
			return agentproto.FileEntry{}, ErrNotFile
		}
	}
	return f.writeStream(p, r, max, overwrite)
}

func (f *FS) writeStream(p string, r io.Reader, max int64, overwrite bool) (agentproto.FileEntry, error) {
	dir := path.Dir(p)
	if dir == "." {
		dir = ""
	}
	if dir != "" {
		info, err := f.root.Stat(dir)
		if err != nil {
			return agentproto.FileEntry{}, mapErr(err)
		}
		if !info.IsDir() {
			return agentproto.FileEntry{}, ErrNotDir
		}
	}
	tmp := tmpName(dir)
	fh, err := f.root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return agentproto.FileEntry{}, mapErr(err)
	}
	cleanup := func() { _ = f.root.Remove(tmp) }
	n, err := io.Copy(fh, io.LimitReader(r, max+1))
	if cerr := fh.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		cleanup()
		return agentproto.FileEntry{}, err
	}
	if n > max {
		cleanup()
		return agentproto.FileEntry{}, ErrTooLarge
	}
	if err := f.root.Lchown(tmp, f.uid, f.gid); err != nil {
		cleanup()
		return agentproto.FileEntry{}, err
	}
	if !overwrite {
		if _, err := f.root.Lstat(p); err == nil {
			cleanup()
			return agentproto.FileEntry{}, ErrExists
		}
	}
	if err := f.root.Rename(tmp, p); err != nil {
		cleanup()
		return agentproto.FileEntry{}, mapErr(err)
	}
	return f.Stat(p)
}

// OpenDownload opens a regular file for streaming.
func (f *FS) OpenDownload(p string) (*os.File, fs.FileInfo, error) {
	return f.openRegular(p)
}
