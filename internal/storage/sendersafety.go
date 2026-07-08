package storage

import (
	"strings"

	"gclean/internal/models"
)

// SenderSafety aggregates one row per distinct sender. DeleteCount/DeleteBytes
// come from VerdictDelete after a dry-run pass. KeepCount is VerdictKeep +
// VerdictProtected (messages that won't be touched). Used by `gclean sender`
// and the experimental Bubble Tea TUI.
type SenderSafety struct {
	Email       string
	TotalCount  int64
	TotalBytes  int64
	DeleteCount int64
	DeleteBytes int64
	KeepCount   int64
	Reasons     []string // distinct junk_reason values seen for this sender
}

// SenderSafety reads the messages table and returns one row per sender,
// ordered by DeleteBytes DESC so the biggest storage wins surface at the top.
func (s *Store) SenderSafety() ([]SenderSafety, error) {
	delV := int(models.VerdictDelete)
	rows, err := s.db.Query(`
		SELECT
			sender_email,
			COUNT(*),
			COALESCE(SUM(size), 0),
			COALESCE(SUM(CASE WHEN verdict = ? THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN verdict = ? THEN size ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN verdict = 0 OR verdict = 3 THEN 1 ELSE 0 END), 0),
			GROUP_CONCAT(DISTINCT junk_reason)
		FROM messages
		GROUP BY sender_email
		ORDER BY 5 DESC
		LIMIT 200
	`, delV, delV)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	out := []SenderSafety{}
	for rows.Next() {
		var ss SenderSafety
		var reasonsStr string
		if err := rows.Scan(&ss.Email, &ss.TotalCount, &ss.TotalBytes, &ss.DeleteCount, &ss.DeleteBytes, &ss.KeepCount, &reasonsStr); err != nil {
			return nil, err
		}
		ss.Reasons = splitReasons(reasonsStr)
		out = append(out, ss)
	}
	return out, rows.Err()
}

func splitReasons(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p == "" {
			continue
		}
		out = append(out, p)
	}
	return out
}
