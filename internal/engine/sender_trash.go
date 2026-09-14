package engine

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/mail"
	"sort"
	"strings"

	"gclean/internal/models"
	"gclean/internal/storage"
)

// SenderTrashPreview is the exact Gmail cohort shown before a sender cleanup.
// ID changes whenever the matching message IDs change.
type SenderTrashPreview struct {
	Sender string
	Query  string
	Count  int
	Bytes  int64
	ID     string

	records []storage.StoredMessage
}

// SenderMessageReader includes Spam in this targeted read. The query itself
// excludes Trash so an existing trashed message is never added to a new batch.
type SenderMessageReader interface {
	ListMessagesIncludingSpam(query string, max int) ([]*models.Message, error)
}

// NormalizeSenderAddress validates a plain mailbox address and returns its
// lower-case form. Display names and query syntax are rejected so the address
// cannot broaden the Gmail search expression.
func NormalizeSenderAddress(input string) (string, error) {
	address := strings.TrimSpace(input)
	if address == "" {
		return "", errors.New("sender address is required")
	}
	parsed, err := mail.ParseAddress(address)
	if err != nil || parsed.Name != "" || parsed.Address != address {
		return "", errors.New("sender must be one plain email address")
	}
	at := strings.LastIndexByte(address, '@')
	if at <= 0 || at == len(address)-1 || !safeLocalPart(address[:at]) || !safeDomain(address[at+1:]) {
		return "", errors.New("sender must be one plain email address")
	}
	return strings.ToLower(address), nil
}

// PreviewSenderTrash lists every Gmail result for sender, then narrows it to
// an exact normalized From address. Gmail's from: operator can match broadly;
// the local equality check is the safety boundary.
func PreviewSenderTrash(reader SenderMessageReader, account, input string) (SenderTrashPreview, error) {
	sender, err := NormalizeSenderAddress(input)
	if err != nil {
		return SenderTrashPreview{}, err
	}
	query := fmt.Sprintf(`from:"%s" -in:trash`, sender)
	messages, err := reader.ListMessagesIncludingSpam(query, 0)
	if err != nil {
		return SenderTrashPreview{}, fmt.Errorf("preview sender %s: %w", sender, err)
	}
	records := make([]storage.StoredMessage, 0, len(messages))
	var bytes int64
	for _, message := range messages {
		candidate, err := NormalizeSenderAddress(message.Sender.Email)
		if err != nil || candidate != sender {
			continue
		}
		classified := Classify(message)
		records = append(records, storage.FromClassified(&classified, models.VerdictDelete))
		bytes += message.Size
	}
	return SenderTrashPreview{
		Sender:  sender,
		Query:   query,
		Count:   len(records),
		Bytes:   bytes,
		ID:      senderTrashPreviewID(account, sender, records),
		records: records,
	}, nil
}

// ApplySenderTrash moves the already-previewed cohort through the Mutation
// Journal, preserving the normal undo and partial-reconciliation guarantees.
func ApplySenderTrash(journal *Reconciler, preview SenderTrashPreview) (Outcome, error) {
	if len(preview.records) == 0 {
		return Outcome{}, nil
	}
	return journal.Apply(Intent{Mutation: MutationTrash, Records: preview.records})
}

func senderTrashPreviewID(account, sender string, records []storage.StoredMessage) string {
	ids := make([]string, len(records))
	for i, record := range records {
		ids[i] = record.ID
	}
	sort.Strings(ids)
	hash := sha256.New()
	_, _ = hash.Write([]byte(strings.ToLower(account)))
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write([]byte(sender))
	for _, id := range ids {
		_, _ = hash.Write([]byte{0})
		_, _ = hash.Write([]byte(id))
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func safeLocalPart(local string) bool {
	for _, r := range local {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune(".!#$%&'*+-/=?^_`{|}~", r) {
			continue
		}
		return false
	}
	return true
}

func safeDomain(domain string) bool {
	if !strings.Contains(domain, ".") || strings.HasPrefix(domain, ".") || strings.HasSuffix(domain, ".") || strings.Contains(domain, "..") {
		return false
	}
	for _, label := range strings.Split(domain, ".") {
		if label == "" || strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
			return false
		}
		for _, r := range label {
			if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' {
				continue
			}
			return false
		}
	}
	return true
}
