package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gclean/internal/defang"
	"gclean/internal/models"
	"gclean/internal/storage"
)

func TestTrashSenderPreviewsThenRequiresYes(t *testing.T) {
	tmp := t.TempDir()
	cachePath := filepath.Join(tmp, "undo.json")
	t.Setenv("GCLEAN_DB_PATH", filepath.Join(tmp, "gclean.db"))
	t.Setenv("GCLEAN_UNDO_CACHE", cachePath)
	wanted := defang.MkEmail("notification", "example.com")
	lookalike := defang.MkEmail("othernotification", "example.com")
	messages := []*models.Message{
		{ID: "wanted", Sender: models.Sender{Email: wanted}, Subject: "Wanted", Date: time.Now(), Size: 1024},
		{ID: "lookalike", Sender: models.Sender{Email: lookalike}, Subject: "Must stay", Date: time.Now(), Size: 2048},
	}
	data, err := json.Marshal(messages)
	if err != nil {
		t.Fatal(err)
	}
	fixturePath := filepath.Join(tmp, "messages.json")
	if err := os.WriteFile(fixturePath, data, 0o600); err != nil {
		t.Fatal(err)
	}

	var output bytes.Buffer
	cmd := Build(&output, &output)
	cmd.SetArgs([]string{"trash-sender", strings.ToUpper(wanted), "--fixtures", fixturePath})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "Found 1 messages") || !strings.Contains(output.String(), "Nothing changed") {
		t.Fatalf("preview output = %q", output.String())
	}
	fields := strings.Fields(output.String())
	var previewID string
	for index, field := range fields {
		if field == "--preview-id" && index+1 < len(fields) {
			previewID = fields[index+1]
			break
		}
	}
	if previewID == "" {
		t.Fatalf("preview output did not include --preview-id: %q", output.String())
	}
	if batch, err := storage.LoadUndoBatch(cachePath); err != nil || len(batch.Records) != 0 {
		t.Fatalf("preview wrote undo state: batch=%+v err=%v", batch, err)
	}

	output.Reset()
	cmd = Build(&output, &output)
	cmd.SetArgs([]string{"trash-sender", wanted, "--yes", "--fixtures", fixturePath})
	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "preview changed or was not supplied") {
		t.Fatalf("missing preview ID error = %v", err)
	}
	if batch, err := storage.LoadUndoBatch(cachePath); err != nil || len(batch.Records) != 0 {
		t.Fatalf("unconfirmed apply wrote undo state: batch=%+v err=%v", batch, err)
	}

	changedData, err := json.Marshal(append(messages, &models.Message{
		ID: "new-arrival", Sender: models.Sender{Email: wanted}, Subject: "New", Date: time.Now(), Size: 512,
	}))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fixturePath, changedData, 0o600); err != nil {
		t.Fatal(err)
	}
	output.Reset()
	cmd = Build(&output, &output)
	cmd.SetArgs([]string{"trash-sender", wanted, "--preview-id", previewID, "--yes", "--fixtures", fixturePath})
	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "preview changed") {
		t.Fatalf("stale preview ID error = %v", err)
	}
	if batch, err := storage.LoadUndoBatch(cachePath); err != nil || len(batch.Records) != 0 {
		t.Fatalf("stale apply wrote undo state: batch=%+v err=%v", batch, err)
	}
	if err := os.WriteFile(fixturePath, data, 0o600); err != nil {
		t.Fatal(err)
	}

	output.Reset()
	cmd = Build(&output, &output)
	cmd.SetArgs([]string{"trash-sender", wanted, "--preview-id", previewID, "--yes", "--fixtures", fixturePath})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "Moved 1 messages") {
		t.Fatalf("apply output = %q", output.String())
	}
	batch, err := storage.LoadUndoBatch(cachePath)
	if err != nil {
		t.Fatal(err)
	}
	if len(batch.Records) != 1 || batch.Records[0].ID != "wanted" {
		t.Fatalf("undo batch = %+v, want only exact sender", batch)
	}
}

func TestTrashSenderRejectsQueryInputBeforeClientResolution(t *testing.T) {
	var output bytes.Buffer
	cmd := Build(&output, &output)
	cmd.SetArgs([]string{"trash-sender", "user@example.com OR from:other@example.com", "--yes"})
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "plain email address") {
		t.Fatalf("error = %v, want address validation", err)
	}
}
