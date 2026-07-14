//go:build windows

package hygiene

func readDiskUsage(string) (DiskUsage, error) {
	return DiskUsage{}, nil
}
