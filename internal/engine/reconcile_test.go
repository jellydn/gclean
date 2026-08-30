package engine

import (
	"errors"
	"path/filepath"
	"reflect"
	"testing"

	"gclean/internal/storage"
)

type journalClient map[string]bool

func (state journalClient) TrashMessages(ids []string) error { return nil }

func (state journalClient) RestoreFromTrash(ids []string) ([]string, error) { return ids, nil }

func (state journalClient) EmptyTrash() error { return nil }

func (state journalClient) InTrash(ids []string) ([]string, error) {
	trashed := make([]string, 0, len(ids))
	for _, id := range ids {
		if state[id] {
			trashed = append(trashed, id)
		}
	}
	return trashed, nil
}

func TestMutationJournalApplyReportsAndCommitsPartialTrash(t *testing.T) {
	store, err := storage.Open(filepath.Join(t.TempDir(), "gclean.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()

	records := []storage.StoredMessage{{ID: "m1"}, {ID: "m2"}}
	for _, record := range records {
		if err := store.Upsert(record); err != nil {
			t.Fatal(err)
		}
	}
	cachePath := filepath.Join(t.TempDir(), "undo-cache.json")
	if err := storage.SaveUndoCache(cachePath, records); err != nil {
		t.Fatal(err)
	}
	serverState := journalClient{}
	journal := Reconciler{Store: store, CachePath: cachePath, Client: serverState}

	outcome, err := journal.Apply(Intent{Mutation: MutationTrash, Records: records}, func() error {
		serverState["m1"] = true
		return errors.New("injected failure")
	})
	if err == nil {
		t.Fatal("Apply() error = nil, want partial mutation error")
	}
	if !reflect.DeepEqual(outcome.Moved, []string{"m1"}) || !reflect.DeepEqual(outcome.StillInTrash, []string{"m1"}) {
		t.Fatalf("outcome = %+v, want m1 moved and still recoverable", outcome)
	}

	remaining, err := store.AllClassified()
	if err != nil {
		t.Fatal(err)
	}
	if len(remaining) != 1 || remaining[0].Message.ID != "m2" {
		t.Fatalf("store remaining = %+v, want only m2", remaining)
	}
	cached, err := storage.LoadUndoCache(cachePath)
	if err != nil {
		t.Fatal(err)
	}
	if len(cached) != 1 || cached[0].ID != "m1" {
		t.Fatalf("cache = %+v, want only m1", cached)
	}
}
