package storage

import (
	"strconv"

	"gclean/internal/models"
)

// Aggregate computes the §5 StatsReport from the messages table.
func (s *Store) Aggregate() (models.StatsReport, error) {
	var r models.StatsReport
	r.ByCategory = map[string]int64{}
	r.ByYear = map[int]int64{}

	rows, err := s.db.Query(`SELECT sender_email, subject, date, size, labels, junk_reason, is_junk FROM messages`)
	if err != nil {
		return r, err
	}
	defer func() { _ = rows.Close() }()

	senderCount := map[string]int64{}
	senderBytes := map[string]int64{}
	categoryCount := map[string]int64{}

	for rows.Next() {
		var (
			sender, subject, dateStr, labels, junkReason string
			size                                         int64
			isJunk                                       int
		)
		if err := rows.Scan(&sender, &subject, &dateStr, &size, &labels, &junkReason, &isJunk); err != nil {
			return r, err
		}
		r.TotalMessages++
		r.EstimatedStorage += size
		senderCount[sender]++
		senderBytes[sender] += size

		switch junkReason {
		case models.ReasonNewsletter, models.ReasonMailingList:
			r.NewsletterCount++
		}
		switch junkReason {
		case models.ReasonGitHub, models.ReasonGitLab,
			models.ReasonJira, models.ReasonSlack, models.ReasonNoreply, models.ReasonBulk,
			models.ReasonAzureAlert:
			r.NotificationCount++
		}
		if size > 10*1024*1024 {
			r.AttachmentsOver10MB++
		}
		for _, l := range splitCSV(labels) {
			categoryCount[l]++
		}
		if len(dateStr) >= 4 {
			if y, err := strconv.Atoi(dateStr[:4]); err == nil {
				r.ByYear[y]++
			}
		}
	}
	if err := rows.Err(); err != nil {
		return r, err
	}

	var topSender string
	var topCount int64
	for k, v := range senderCount {
		if v > topCount {
			topCount = v
			topSender = k
		}
	}
	r.LargestSender = models.SenderVolume{Email: topSender, Count: topCount, Bytes: senderBytes[topSender]}
	r.ByCategory = bucketByCategory(categoryCount)
	return r, nil
}

// PotentialReclaimOf computes bytes that would be reclaimed for the given
// verdict value (matches models.Verdict*).
func (s *Store) PotentialReclaimOf(verdict int) (int64, error) {
	var total int64
	if err := s.db.QueryRow(`SELECT COALESCE(SUM(size),0) FROM messages WHERE verdict = ?`, verdict).Scan(&total); err != nil {
		return 0, err
	}
	return total, nil
}

// SendersByVolume returns senders ranked by total bytes (descending), limited.
func (s *Store) SendersByVolume(limit int) ([]models.SenderVolume, error) {
	rows, err := s.db.Query(`SELECT sender_email, COUNT(*), COALESCE(SUM(size),0) FROM messages GROUP BY sender_email ORDER BY 3 DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []models.SenderVolume{}
	for rows.Next() {
		var sv models.SenderVolume
		if err := rows.Scan(&sv.Email, &sv.Count, &sv.Bytes); err != nil {
			return nil, err
		}
		out = append(out, sv)
	}
	return out, rows.Err()
}

// LargestAttachments returns the top N candidates with size above threshold.
func (s *Store) LargestAttachments(minBytes int64, limit int) ([]StoredMessage, error) {
	rows, err := s.db.Query(`SELECT id, thread_id, sender_email, sender_name, is_contact, subject, date, size, labels, headers, junk_reason, is_junk, protected, verdict, verdict_reasons
		FROM messages WHERE size >= ? ORDER BY size DESC LIMIT ?`, minBytes, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []StoredMessage{}
	for rows.Next() {
		var (
			m                            StoredMessage
			isContact, isJunk, protected int
		)
		if err := rows.Scan(&m.ID, &m.ThreadID, &m.SenderEmail, &m.SenderName, &isContact, &m.Subject, &m.Date, &m.Size,
			&m.Labels, &m.Headers, &m.JunkReason, &isJunk, &protected, &m.Verdict, &m.VerdictReasons); err != nil {
			return nil, err
		}
		m.IsContact, m.IsJunk, m.Protected = isContact == 1, isJunk == 1, protected == 1
		out = append(out, m)
	}
	return out, rows.Err()
}

func bucketByCategory(counts map[string]int64) map[string]int64 {
	out := map[string]int64{}
	for k, v := range counts {
		switch k {
		case "CATEGORY_PROMOTIONS":
			out["promotions"] += v
		case "CATEGORY_SOCIAL":
			out["social"] += v
		case "CATEGORY_UPDATES":
			out["updates"] += v
		case "CATEGORY_FORUMS":
			out["forums"] += v
		case "CATEGORY_PERSONAL":
			out["personal"] += v
		default:
			out["other"] += v
		}
	}
	return out
}
