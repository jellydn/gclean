package storage

import (
	"os"
	"path/filepath"
)

// writeFileAtomic writes data to path atomically: it writes and syncs a
// temporary file in the same directory, renames it into place, then syncs the
// directory so the rename is durable across a crash. A crash can therefore
// never leave a partially-written file at path. tmpPrefix must contain a "*",
// which os.CreateTemp replaces with a random suffix.
//
// This is the single crash-safe write primitive for the package's on-disk
// caches (undo cache, sender selection). Before it existed, writeUndoCache
// and writeAtomic each re-implemented the same write+sync+rename+dirsync
// dance, so a durability fix would have had to land twice.
func writeFileAtomic(path string, data []byte, mode os.FileMode, tmpPrefix string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, tmpPrefix)
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer func() {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
	}()
	if err := tmp.Chmod(mode); err != nil {
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return err
	}
	return syncDirectory(dir)
}

// syncDirectory fsyncs a directory so a completed rename is durable across a
// crash.
func syncDirectory(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = dir.Close() }()
	return dir.Sync()
}
