//go:build !windows

package hygiene

import "golang.org/x/sys/unix"

func readDiskUsage(path string) (DiskUsage, error) {
	var stat unix.Statfs_t
	if err := unix.Statfs(path, &stat); err != nil {
		return DiskUsage{}, err
	}
	blockSize := int64(stat.Bsize)
	return DiskUsage{
		Known:         true,
		CapacityBytes: int64(stat.Blocks) * blockSize,
		FreeBytes:     int64(stat.Bavail) * blockSize,
	}, nil
}
