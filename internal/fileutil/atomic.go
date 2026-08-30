// Package fileutil contains small cross-platform filesystem primitives shared
// by configuration and recovery persistence.
package fileutil

import (
	"os"
	"path/filepath"
)

// WriteAtomic writes, flushes, and atomically replaces path with data.
func WriteAtomic(path string, data []byte, mode os.FileMode, tempPattern string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	temp, err := os.CreateTemp(dir, tempPattern)
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer func() {
		_ = temp.Close()
		_ = os.Remove(tempPath)
	}()
	if err := temp.Chmod(mode); err != nil {
		return err
	}
	if _, err := temp.Write(data); err != nil {
		return err
	}
	if err := temp.Sync(); err != nil {
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := replaceFile(tempPath, path); err != nil {
		return err
	}
	return syncDirectory(dir)
}
