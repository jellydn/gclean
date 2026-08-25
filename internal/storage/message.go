package storage

import (
	"encoding/json"
	"strings"
	"time"

	"gclean/internal/models"
)

// FromClassified converts a classified message into the storage-boundary
// record. It is the single adapter between models.Message and the messages
// table (which is also the shape the undo cache persists), so a schema
// change edits exactly one function instead of every write path.
//
// The verdict parameter carries the planner's disposition: VerdictKeep at
// scan time (the pre-plan default) and VerdictDelete when the delete cohort
// is trashed.
func FromClassified(c *models.Classified, verdict models.Verdict) StoredMessage {
	m := c.Message
	return StoredMessage{
		ID:          m.ID,
		ThreadID:    m.ThreadID,
		SenderEmail: m.Sender.Email,
		SenderName:  m.Sender.Name,
		IsContact:   m.Sender.IsContact,
		Subject:     m.Subject,
		Date:        m.Date.Format(time.RFC3339),
		Size:        m.Size,
		Labels:      strings.Join(m.Labels, ","),
		Headers:     encodeHeaders(m.Headers),
		JunkReason:  c.ReasonCode,
		IsJunk:      c.IsJunk,
		Verdict:     int(verdict),
	}
}

func encodeHeaders(h map[string]string) string {
	b, _ := json.Marshal(h)
	return string(b)
}

// FilterRecords returns the records whose ID is in ids, preserving order.
// It is the shared subset operation the reconcile paths use to trim undo
// records to the messages Gmail actually mutated.
func FilterRecords(records []StoredMessage, ids []string) []StoredMessage {
	keep := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		keep[id] = struct{}{}
	}
	out := make([]StoredMessage, 0, len(ids))
	for _, r := range records {
		if _, ok := keep[r.ID]; ok {
			out = append(out, r)
		}
	}
	return out
}
