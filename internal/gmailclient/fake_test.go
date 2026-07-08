package gmailclient

import (
	"testing"
	"time"

	"gclean/internal/models"
)

func TestFakeClient_ListAndTrash(t *testing.T) {
	msgs := []*models.Message{
		{ID: "a", Subject: "alpha", Date: time.Now()},
		{ID: "b", Subject: "beta", Date: time.Now()},
		{ID: "c", Subject: "gamma", Date: time.Now()},
	}
	c := NewFakeClientFromMessages(msgs)

	got, err := c.ListMessages("", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("want 3, got %d", len(got))
	}

	got, err = c.ListMessages("subject:alpha", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != "a" {
		t.Fatalf("query filter broken, got %#v", got)
	}

	if err := c.TrashMessages([]string{"a"}); err != nil {
		t.Fatal(err)
	}
	got, _ = c.ListMessages("", 0)
	if len(got) != 2 {
		t.Fatalf("trashed message should disappear from list, got %d", len(got))
	}

	ids := c.TrashedIDs()
	if len(ids) != 1 || ids[0] != "a" {
		t.Fatalf("TrashedIDs off: %v", ids)
	}
	if err := c.RestoreFromTrash([]string{"a"}); err != nil {
		t.Fatal(err)
	}
	got, _ = c.ListMessages("", 0)
	if len(got) != 3 {
		t.Fatalf("restored message should reappear, got %d", len(got))
	}
}
