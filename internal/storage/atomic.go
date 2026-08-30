package storage

import (
	"os"

	"gclean/internal/fileutil"
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
	return fileutil.WriteAtomic(path, data, mode, tmpPrefix)
}
