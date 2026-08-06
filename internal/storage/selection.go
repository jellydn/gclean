package storage

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type senderSelection struct {
	Senders   []string `json:"senders"`
	Selectors []string `json:"selectors"` // legacy field from the advisory format
	SavedAt   string   `json:"saved_at"`
	Timestamp string   `json:"ts"` // legacy field from the advisory format
}

// SaveSelection persists the sender cohort chosen by the TUI.
func SaveSelection(path string, senders []string) error {
	if len(senders) == 0 {
		return fmt.Errorf("selection must contain at least one sender")
	}
	senders = normalizeSenders(senders)
	if len(senders) == 0 {
		return fmt.Errorf("selection must contain at least one non-empty sender")
	}
	payload, err := json.MarshalIndent(senderSelection{
		Senders: senders,
		SavedAt: time.Now().UTC().Format(time.RFC3339),
	}, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(path, payload, 0o600)
}

// LoadSelection returns the selected senders. A missing file means that
// cleanup is unrestricted; malformed selection data is an error.
func LoadSelection(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var selection senderSelection
	if err := json.Unmarshal(data, &selection); err != nil {
		return nil, fmt.Errorf("parse sender selection: %w", err)
	}
	senders := selection.Senders
	if len(senders) == 0 {
		senders = selection.Selectors
	}
	senders = normalizeSenders(senders)
	if len(senders) == 0 {
		return nil, fmt.Errorf("sender selection is empty")
	}
	return senders, nil
}

func normalizeSenders(senders []string) []string {
	seen := make(map[string]struct{}, len(senders))
	out := make([]string, 0, len(senders))
	for _, sender := range senders {
		sender = strings.TrimSpace(sender)
		if sender == "" {
			continue
		}
		if _, ok := seen[sender]; ok {
			continue
		}
		seen[sender] = struct{}{}
		out = append(out, sender)
	}
	return out
}

func writeAtomic(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if info, err := os.Stat(path); err == nil && info.Size() == 0 {
		// A zero-byte cache is safe to replace; non-empty recovery files are
		// protected by their callers before reaching this helper.
	} else if err != nil && !os.IsNotExist(err) {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".gclean-atomic-*")
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
	dirFile, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer func() { _ = dirFile.Close() }()
	return dirFile.Sync()
}
