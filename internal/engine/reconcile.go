package engine

import (
	"errors"
	"fmt"

	"gclean/internal/storage"
)

// ErrNothingToRestore reports that no recoverable gclean batch exists.
var ErrNothingToRestore = errors.New("nothing to restore")

// MutationClient is the engine's single Gmail mutation adapter. It combines
// each state transition with the read-back operation the journal uses to
// reconcile partial results.
type MutationClient interface {
	TrashMessages(ids []string) error
	RestoreFromTrash(ids []string) ([]string, error)
	EmptyTrash() error
	InTrash(ids []string) ([]string, error)
}

// Mutation identifies the state transition a journal intent requests.
type Mutation string

const (
	MutationTrash   Mutation = "trash"
	MutationRestore Mutation = "restore"
	MutationPurge   Mutation = "purge"
)

// Intent records the local cohort and transition that must stay consistent
// with Gmail while a mutation is applied.
type Intent struct {
	Mutation Mutation
	Records  []storage.StoredMessage
}

// Outcome is the journal's typed account of a mutation. Moved contains IDs
// that crossed the Trash boundary, StillInTrash contains recoverable IDs,
// and Gone contains IDs that Gmail no longer reports in Trash. MovedRecords
// carries the corresponding local records for callers that render details.
type Outcome struct {
	Moved        []string
	MovedRecords []storage.StoredMessage
	StillInTrash []string
	Gone         []string
}

// Reconciler is the mutation journal for clean, undo, and purge. Apply owns
// the mutation/reconcile/local-state protocol so callers only describe an
// intent and render its Outcome.
type Reconciler struct {
	Store     *storage.Store
	CachePath string
	Account   string
	Client    MutationClient
	// MutationLockHeld is set when a caller already holds the lock across a
	// preview and its apply stage.
	MutationLockHeld bool
}

// Apply runs a Gmail mutation and journals its observed outcome into SQLite
// and the undo cache. If Gmail applies only part of a mutation, Apply reads
// server state back, commits exactly that subset locally, and returns both a
// typed Outcome and an operation-specific partial-failure error.
func (r *Reconciler) Apply(intent Intent) (Outcome, error) {
	var lock *storage.MutationLock
	if !r.MutationLockHeld {
		var err error
		lock, err = storage.AcquireMutationLock(r.CachePath)
		if err != nil {
			return Outcome{}, err
		}
		defer func() { _ = lock.Unlock() }()
	}

	if (intent.Mutation == MutationRestore || intent.Mutation == MutationPurge) && r.CachePath != "" {
		batch, err := storage.LoadUndoBatch(r.CachePath)
		if err != nil {
			return Outcome{}, err
		}
		if err := storage.ValidateUndoBatchAccount(batch, r.Account); err != nil {
			return Outcome{}, err
		}
		intent.Records = batch.Records
	}
	if intent.Mutation == MutationRestore && len(intent.Records) == 0 {
		return Outcome{}, ErrNothingToRestore
	}
	if r.Store != nil {
		if err := r.Store.BindAccount(r.Account); err != nil {
			return Outcome{}, err
		}
	}

	switch intent.Mutation {
	case MutationTrash:
		return r.applyTrash(intent.Records)
	case MutationRestore:
		return r.applyRestore(intent.Records)
	case MutationPurge:
		return r.applyPurge(intent.Records)
	default:
		return Outcome{}, fmt.Errorf("unknown mutation %q", intent.Mutation)
	}
}

func (r *Reconciler) applyTrash(records []storage.StoredMessage) (Outcome, error) {
	ids := recordIDs(records)
	if r.CachePath != "" {
		if err := storage.SaveUndoCacheForAccount(r.CachePath, r.Account, records); err != nil {
			return Outcome{}, fmt.Errorf("save undo cache: %w", err)
		}
	}
	cause := r.Client.TrashMessages(ids)
	if cause == nil {
		if err := r.Store.MarkTrashed(ids); err == nil {
			return outcomeFor(records, ids, ids, nil), nil
		} else {
			cause = err
		}
	}

	moved, err := r.inTrash(ids)
	if err != nil {
		return Outcome{}, fmt.Errorf("trash: %w (reconcile failed: %v)", cause, err)
	}
	outcome := outcomeFor(records, moved, moved, nil)
	if err := r.commitTrash(records, moved); err != nil {
		return outcome, fmt.Errorf("trash: %w (reconcile: %v)", cause, err)
	}
	switch {
	case len(moved) == 0:
		return outcome, fmt.Errorf("trash: no messages moved to Trash: %w", cause)
	case len(moved) < len(ids):
		return outcome, fmt.Errorf("trash partially applied: %d of %d messages moved to Trash: %w", len(moved), len(ids), cause)
	default:
		return outcome, fmt.Errorf("trash: %w", cause)
	}
}

func (r *Reconciler) applyRestore(records []storage.StoredMessage) (Outcome, error) {
	ids := recordIDs(records)
	before, err := r.inTrash(ids)
	if err != nil {
		return Outcome{}, fmt.Errorf("restore: reconcile failed: %w", err)
	}

	_, cause := r.Client.RestoreFromTrash(ids)
	still, err := r.inTrash(ids)
	if err != nil {
		if cause != nil {
			return Outcome{}, fmt.Errorf("restore: %w (reconcile failed: %v)", cause, err)
		}
		return Outcome{}, fmt.Errorf("restore: reconcile failed: %w", err)
	}
	moved := difference(before, still)
	gone := difference(ids, before)
	outcome := outcomeFor(records, moved, still, gone)

	if err := r.Store.RestoreTrashed(outcome.MovedRecords); err != nil {
		if cacheErr := r.replaceCache(storage.FilterRecords(records, still)); cacheErr != nil {
			return outcome, fmt.Errorf("restore: %w (reconcile: %v)", err, cacheErr)
		}
		return outcome, fmt.Errorf("restore: %w", err)
	}
	if err := r.replaceCache(storage.FilterRecords(records, still)); err != nil {
		return outcome, fmt.Errorf("restore: reconcile cache rewrite failed: %w", err)
	}
	if cause == nil {
		return outcome, nil
	}
	if len(moved) == 0 {
		return outcome, fmt.Errorf("restore: no messages restored: %w", cause)
	}
	return outcome, fmt.Errorf("restore partially applied: %d of %d messages restored: %w", len(moved), len(ids), cause)
}

func (r *Reconciler) applyPurge(records []storage.StoredMessage) (Outcome, error) {
	ids := recordIDs(records)
	cause := r.Client.EmptyTrash()
	if cause == nil {
		outcome := outcomeFor(records, nil, nil, ids)
		if err := r.replaceCache(nil); err != nil {
			return outcome, fmt.Errorf("purge: remove undo cache: %w", err)
		}
		return outcome, nil
	}
	if len(ids) == 0 {
		return Outcome{}, cause
	}

	still, err := r.inTrash(ids)
	if err != nil {
		return Outcome{}, fmt.Errorf("purge: %w (reconcile failed: %v)", cause, err)
	}
	outcome := outcomeFor(records, nil, still, difference(ids, still))
	if err := r.replaceCache(storage.FilterRecords(records, still)); err != nil {
		return outcome, fmt.Errorf("purge: %w (reconcile cache rewrite failed: %v)", cause, err)
	}
	if len(still) > 0 {
		return outcome, fmt.Errorf("purge partially applied: %d messages remain in Trash: %w", len(still), cause)
	}
	return outcome, cause
}

func (r *Reconciler) commitTrash(records []storage.StoredMessage, moved []string) error {
	if err := r.replaceCache(storage.FilterRecords(records, moved)); err != nil {
		return err
	}
	return r.Store.MarkTrashed(moved)
}

func (r *Reconciler) replaceCache(records []storage.StoredMessage) error {
	if r.CachePath == "" {
		return nil
	}
	return storage.ReplaceOrRemoveUndoCacheForAccount(r.CachePath, r.Account, records)
}

func (r *Reconciler) inTrash(ids []string) ([]string, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	return r.Client.InTrash(ids)
}

func outcomeFor(records []storage.StoredMessage, moved, still, gone []string) Outcome {
	return Outcome{
		Moved:        moved,
		MovedRecords: storage.FilterRecords(records, moved),
		StillInTrash: still,
		Gone:         gone,
	}
}

func difference(ids, excluded []string) []string {
	skip := make(map[string]struct{}, len(excluded))
	for _, id := range excluded {
		skip[id] = struct{}{}
	}
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if _, ok := skip[id]; !ok {
			out = append(out, id)
		}
	}
	return out
}

func recordIDs(records []storage.StoredMessage) []string {
	ids := make([]string, 0, len(records))
	for _, record := range records {
		ids = append(ids, record.ID)
	}
	return ids
}
