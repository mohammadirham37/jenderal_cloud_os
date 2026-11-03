//go:build unix

package storage

import "syscall"

func statfs(path string) (used, total int64, err error) {
	var s syscall.Statfs_t
	if err := syscall.Statfs(path, &s); err != nil {
		return 0, 0, err
	}
	bs := int64(s.Bsize)
	total = int64(s.Blocks) * bs
	used = total - int64(s.Bfree)*bs
	return used, total, nil
}
