package engine

import (
	"fmt"

	"gclean/internal/storage"
)

// ReadBack is the narrow Gmail seam the reconcile core needs: after a
// mutation fails partway, it asks Gmail what actually moved so local state
// (SQLite + undo cache) can be trimmed to match. Only InTrash is required —
// the mutation calls stay on the caller's client, so the reconcile core is
// testable against a fake that implements nothing else.
type ReadBack interface {
	InTrash(ids []string) ([]string, error)
}

// Gmail is the mutation+read-back surface undo and purge need on top of
// ReadBack. It is a strict subset of gmailclient.Client (no ListMessages),
// so the engine stays free of the gmailclient import graph and the undo/purge
// flows never reach for the full Client.
type Gmail interface {
	RestoreFromTrash(ids []string) ([]string, error)
	EmptyTrash() error
	InTrash(ids []string) ([]string, error)
}

// Reconciler owns "reconcile local state against what Gmail actually did
// after a mutation" for clean, undo, and purge. Before this module the
// concept lived in two packages with two shapes: the engine's reconcileTrash
// (clean) and the CLI's undoWithReconcile / purgeWithReconcile (undo/purge),
// which reached for the full 5-method Client. One home, one vocabulary; the
// Gmail seam is passed per call so each method's dependency is explicit.
type Reconciler struct {
	// Store is the local SQLite store. Required by ReconcileTrash and Undo
	// (store mark / re-insert); Purge does not touch the store, so it may
	// leave Store nil.
	Store     *storage.Store
	CachePath string
}

// ReconcileTrash trims the undo cache and the local mark so they reflect only
// the messages that actually reached Gmail's Trash after a partial failure.
// It fails loudly if the cache cannot be rewritten, and returns the kept
// records so the caller can render exactly what survived without recomputing
// the subset.
func (r *Reconciler) ReconcileTrash(records []storage.StoredMessage, trashed []string) ([]storage.StoredMessage, error) {
	kept := storage.FilterRecords(records, trashed)
	if r.CachePath != "" {
		if err := storage.ReplaceOrRemoveUndoCache(r.CachePath, kept); err != nil {
			return nil, err
		}
	}
	return kept, r.Store.MarkTrashed(trashed)
}

// ReconcileTrashFailure reconciles a failed trash against Gmail's actual
// state: it asks which ids reached Trash, trims the undo cache and the local
// mark to match, and wraps the original failure with a named prefix. It
// returns the ids actually in Trash and their records so the caller can
// report partial progress.
func (r *Reconciler) ReconcileTrashFailure(gmail ReadBack, records []storage.StoredMessage, ids []string, prefix string, cause error) ([]string, []storage.StoredMessage, error) {
	trashed, inErr := gmail.InTrash(ids)
	if inErr != nil {
		return nil, nil, fmt.Errorf("%s: %w (reconcile failed: %v)", prefix, cause, inErr)
	}
	kept, err := r.ReconcileTrash(records, trashed)
	if err != nil {
		return nil, nil, fmt.Errorf("%s: %w (reconcile: %v)", prefix, cause, err)
	}
	return trashed, kept, nil
}

// Undo restores records from Trash, reconciling so the local store and undo
// cache reflect Gmail's actual state. RestoreFromTrash returns the ids it
// actually untrashed (404s — permanently deleted messages, e.g. the aftermath
// of a partial purge — are skipped, not errors), so only those are
// re-inserted; the cache is trimmed to what is still in Trash (or removed
// entirely) so `gclean undo` can be retried and can never point at ghosts. It
// returns the number of messages actually restored.
func (r *Reconciler) Undo(gmail Gmail, records []storage.StoredMessage) (int, error) {
	ids := recordIDs(records)
	restored, restoreErr := gmail.RestoreFromTrash(ids)
	if restoreErr != nil {
		// Reconcile a partial restore: re-insert what actually moved out of
		// Trash before the failure, trim the cache to what is still in Trash
		// so undo can retry, and drop ids in neither set (permanently
		// deleted) without re-inserting them.
		still, inErr := gmail.InTrash(ids)
		if inErr != nil {
			return 0, fmt.Errorf("restore: %w (reconcile failed: %v)", restoreErr, inErr)
		}
		if err := r.Store.RestoreTrashed(storage.FilterRecords(records, restored)); err != nil {
			return 0, fmt.Errorf("restore: %w (reconcile re-insert failed: %v)", restoreErr, err)
		}
		if err := storage.ReplaceOrRemoveUndoCache(r.CachePath, storage.FilterRecords(records, still)); err != nil {
			return 0, fmt.Errorf("restore: %w (reconcile cache rewrite failed: %v)", restoreErr, err)
		}
		if len(restored) == 0 {
			return 0, fmt.Errorf("restore: no messages restored: %w", restoreErr)
		}
		return 0, fmt.Errorf("restore partially applied: %d of %d messages restored: %w", len(restored), len(ids), restoreErr)
	}
	// RestoreFromTrash succeeded: every id was either untrashed or 404
	// (permanently deleted). Re-insert exactly the restored ones.
	if err := r.Store.RestoreTrashed(storage.FilterRecords(records, restored)); err != nil {
		// Gmail moved the messages but the local re-insert failed; reconcile
		// the cache against what Gmail still reports in Trash.
		still, inErr := gmail.InTrash(ids)
		if inErr != nil {
			return 0, fmt.Errorf("restore: %w (reconcile failed: %v)", err, inErr)
		}
		if rerr := storage.ReplaceOrRemoveUndoCache(r.CachePath, storage.FilterRecords(records, still)); rerr != nil {
			return 0, fmt.Errorf("restore: %w (reconcile: %v)", err, rerr)
		}
		return 0, fmt.Errorf("restore: %w", err)
	}
	// Nothing remains in Trash; a cache file with zero records would block a
	// retried clean, so remove it rather than write an empty one.
	if err := storage.ReplaceOrRemoveUndoCache(r.CachePath, nil); err != nil {
		return 0, fmt.Errorf("restore: remove undo cache: %w", err)
	}
	return len(restored), nil
}

// Purge empties Trash, keeping (and trimming) the undo cache to the messages
// still in Trash on a partial failure so `gclean undo` can still recover
// them. If the cohort is fully purged (InTrash finds nothing, e.g. the
// failing page came after the gclean cohort was deleted), the stale cache is
// removed so undo cannot point at permanently deleted IDs. A full success
// also removes the cache.
func (r *Reconciler) Purge(gmail Gmail, records []storage.StoredMessage) error {
	if err := gmail.EmptyTrash(); err != nil {
		if len(records) > 0 {
			still, inErr := gmail.InTrash(recordIDs(records))
			if inErr != nil {
				return fmt.Errorf("purge: %w (reconcile failed: %v)", err, inErr)
			}
			if len(still) > 0 {
				if err2 := storage.ReplaceUndoCache(r.CachePath, storage.FilterRecords(records, still)); err2 != nil {
					return fmt.Errorf("purge: %w (reconcile cache rewrite failed: %v)", err, err2)
				}
				return fmt.Errorf("purge partially applied: %d messages remain in Trash: %w", len(still), err)
			}
			if err2 := storage.ReplaceOrRemoveUndoCache(r.CachePath, nil); err2 != nil {
				return fmt.Errorf("purge: %w (reconcile cache remove failed: %v)", err, err2)
			}
		}
		return err
	}
	// Full success: nothing left in Trash; remove the cache so undo cannot
	// point at permanently deleted IDs.
	if err := storage.ReplaceOrRemoveUndoCache(r.CachePath, nil); err != nil {
		return fmt.Errorf("purge: remove undo cache: %w", err)
	}
	return nil
}

// recordIDs extracts the message IDs from undo-cache records.
func recordIDs(records []storage.StoredMessage) []string {
	ids := make([]string, 0, len(records))
	for _, r := range records {
		ids = append(ids, r.ID)
	}
	return ids
}
