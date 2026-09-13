//go:build darwin

package scan

import (
	"io/fs"
	"syscall"
)

// statOf extracts device, inode, link count and allocated bytes from an lstat result.
func statOf(fi fs.FileInfo) (dev, ino uint64, nlink uint32, bytes int64, ok bool) {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, 0, 0, fi.Size(), false
	}
	return uint64(st.Dev), st.Ino, uint32(st.Nlink), st.Blocks * 512, true
}
