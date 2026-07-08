package storage

// SenderSafety aggregates one row per distinct sender. DeleteCount/DeleteBytes
// come from VerdictDelete after a dry-run pass. KeepCount is VerdictKeep +
// VerdictProtected (messages that won't be touched). Produced by
// Store.Aggregations (a single messages-table scan); see stats.go. Used by the
// experimental Bubble Tea TUI.
type SenderSafety struct {
	Email       string
	TotalCount  int64
	TotalBytes  int64
	DeleteCount int64
	DeleteBytes int64
	KeepCount   int64
	Reasons     []string // distinct junk_reason values seen for this sender
}
