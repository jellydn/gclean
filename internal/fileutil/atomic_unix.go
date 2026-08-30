//go:build !windows

package fileutil

import "os"

func replaceFile(source, destination string) error { return os.Rename(source, destination) }

func syncDirectory(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = dir.Close() }()
	return dir.Sync()
}
