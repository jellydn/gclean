// Package storage is gclean's local persistence layer. The MVP store is
// metadata only — never message bodies — honoring the §15 local-only default.
// SQLite via modernc.org/sqlite keeps builds CGO-free.
package storage

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	_ "modernc.org/sqlite"

	"gclean/internal/models"
)

// schema is intentionally small. We never store bodies, snippets beyond what
// Gmail itself surfaces, or any content the user did not choose to enrich.
const schema = `
CREATE TABLE IF NOT EXISTS messages (
    id TEXT PRIMARY KEY,
    thread_id TEXT,
    sender_email TEXT,
    sender_name TEXT,
    is_contact INTEGER,
    subject TEXT,
    date DATETIME,
    size INTEGER,
    labels TEXT,
    headers TEXT,
    junk_reason TEXT,
    is_junk INTEGER,
    protected INTEGER,
    verdict INTEGER,
    verdict_reasons TEXT
);
CREATE INDEX IF NOT EXISTS idx_messages_sender ON messages(sender_email);
CREATE INDEX IF NOT EXISTS idx_messages_date ON messages(date);
CREATE INDEX IF NOT EXISTS idx_messages_junk ON messages(is_junk);
CREATE INDEX IF NOT EXISTS idx_messages_verdict ON messages(verdict);
`

// Store wraps *sql.DB. Construct via Open(path).
type Store struct {
	db *sql.DB
}

// Open opens (or creates) the SQLite database at path and applies migrations.
func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	if _, err := db.Exec(schema); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return &Store{db: db}, nil
}

// Close flushes and releases the DB.
func (s *Store) Close() error { return s.db.Close() }

// Upsert writes or updates a stored message row keyed by ID.
func (s *Store) Upsert(m StoredMessage) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	if err := upsertMsg(tx, m); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

// SetVerdict stamps the planner's verdict for a message.
func (s *Store) SetVerdict(id string, verdict int, reasons string, protected bool) error {
	_, err := s.db.Exec(`UPDATE messages SET verdict=?, verdict_reasons=?, protected=? WHERE id=?`,
		verdict, reasons, boolInt(protected), id)
	return err
}

// CountAll returns the number of stored messages.
func (s *Store) CountAll() (int64, error) {
	var n int64
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM messages`).Scan(&n); err != nil {
		return 0, fmt.Errorf("count: %w", err)
	}
	return n, nil
}

// AllClassified reads all rows back as models.Classified for re-planning.
func (s *Store) AllClassified() ([]*models.Classified, error) {
	rows, err := s.db.Query(`SELECT id, thread_id, sender_email, sender_name, subject, date, size, labels, headers, junk_reason, is_junk, is_contact FROM messages`)
	if err != nil {
		return nil, fmt.Errorf("select: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := []*models.Classified{}
	for rows.Next() {
		var (
			id, threadID, senderEmail, senderName, subject, dateStr string
			size                                                    int64
			labelsCSV, headersJSON, junkReason                      string
			isJunk, isContact                                       int
		)
		if err := rows.Scan(&id, &threadID, &senderEmail, &senderName, &subject, &dateStr, &size,
			&labelsCSV, &headersJSON, &junkReason, &isJunk, &isContact); err != nil {
			return nil, err
		}
		dt, _ := time.Parse(time.RFC3339, dateStr)
		out = append(out, &models.Classified{
			Message: &models.Message{
				ID:       id,
				ThreadID: threadID,
				Sender: models.Sender{
					Email:     senderEmail,
					Name:      senderName,
					IsContact: isContact == 1,
				},
				Subject: subject,
				Date:    dt,
				Size:    size,
				Labels:  splitCSV(labelsCSV),
				Headers: decodeHeaders(headersJSON),
			},
			IsJunk:     isJunk == 1,
			ReasonCode: junkReason,
		})
	}
	return out, rows.Err()
}

// DeleteMessageIDs returns the IDs of messages with a given verdict.
func (s *Store) DeleteMessageIDs() ([]string, error) {
	rows, err := s.db.Query(`SELECT id FROM messages WHERE verdict = ?`, int(models.VerdictDelete))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// MarkTrashed records that a message was moved to Trash so subsequent
// scans don't re-include it.
func (s *Store) MarkTrashed(ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	stmt, err := tx.Prepare(`DELETE FROM messages WHERE id=?`)
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	defer func() { _ = stmt.Close() }()
	for _, id := range ids {
		if _, err := stmt.Exec(id); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}

// RestoreTrashed inserts the previously trashed messages back into the
// store. Caller is responsible for providing the original StoredMessage
// records (we cache them in memory at trash time so a real TrashMessage's
// data is preserved across the undo window — locally we accept that
// drag-along via the caller; the FakeClient records them).
func (s *Store) RestoreTrashed(restored []StoredMessage) error {
	if len(restored) == 0 {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	for _, m := range restored {
		if err := upsertMsg(tx, m); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}

func upsertMsg(tx *sql.Tx, m StoredMessage) error {
	_, err := tx.Exec(`
INSERT INTO messages(id, thread_id, sender_email, sender_name, is_contact, subject, date, size, labels, headers, junk_reason, is_junk, protected, verdict, verdict_reasons)
VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
ON CONFLICT(id) DO UPDATE SET
    thread_id=excluded.thread_id,
    sender_email=excluded.sender_email,
    sender_name=excluded.sender_name,
    is_contact=excluded.is_contact,
    subject=excluded.subject,
    date=excluded.date,
    size=excluded.size,
    labels=excluded.labels,
    headers=excluded.headers,
    junk_reason=excluded.junk_reason,
    is_junk=excluded.is_junk`,
		m.ID, m.ThreadID, m.SenderEmail, m.SenderName, boolInt(m.IsContact),
		m.Subject, m.Date, m.Size, m.Labels, m.Headers, m.JunkReason,
		boolInt(m.IsJunk), boolInt(m.Protected), m.Verdict, m.VerdictReasons,
	)
	return err
}

// StoredMessage mirrors the columns of the `messages` table. It's the type
// used at the storage boundary.
type StoredMessage struct {
	ID, ThreadID, SenderEmail, SenderName, Subject, Date string
	IsContact                                            bool
	Size                                                 int64
	Labels, Headers, JunkReason, VerdictReasons          string
	IsJunk, Protected                                    bool
	Verdict                                              int
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func splitCSV(s string) []string {
	if s == "" {
		return nil
	}
	out := []string{}
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == ',' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	out = append(out, s[start:])
	return out
}

func decodeHeaders(j string) map[string]string {
	if j == "" {
		return map[string]string{}
	}
	m := map[string]string{}
	_ = json.Unmarshal([]byte(j), &m)
	return m
}
