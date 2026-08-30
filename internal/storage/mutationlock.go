package storage

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/gofrs/flock"
)

// MutationLock serializes Gmail mutations that share an undo-cache path,
// including mutations performed by separate gclean processes.
type MutationLock struct {
	file *flock.Flock
}

// AcquireMutationLock takes a non-blocking OS-level lock. Keeping the lock
// alongside the undo cache naturally scopes it to the same account workspace.
func AcquireMutationLock(cachePath string) (*MutationLock, error) {
	if cachePath == "" {
		return &MutationLock{}, nil
	}
	if err := os.MkdirAll(filepath.Dir(cachePath), 0o700); err != nil {
		return nil, fmt.Errorf("create mutation lock directory: %w", err)
	}
	file := flock.New(filepath.Clean(cachePath) + ".lock")
	locked, err := file.TryLock()
	if err != nil {
		return nil, fmt.Errorf("acquire mutation lock: %w", err)
	}
	if !locked {
		return nil, fmt.Errorf("another gclean mutation is already running for %s", cachePath)
	}
	return &MutationLock{file: file}, nil
}

// Unlock releases the cross-process mutation lock.
func (l *MutationLock) Unlock() error {
	if l == nil || l.file == nil {
		return nil
	}
	return l.file.Unlock()
}
