package engine

import (
	"strings"
	"time"

	"gclean/internal/models"
)

// ProtectResult indicates whether a message should be kept out of any
// cleanup operation and why.
type ProtectResult struct {
	Protected bool
	Reason    string
}

// KeepConfig matches §6. Stored in config.yaml; the Doc.Keep unmarshals into
// this struct via yaml tags.
type KeepConfig struct {
	Contacts   bool `yaml:"contacts" json:"contacts"`
	Replied    bool `yaml:"replied" json:"replied"`
	Starred    bool `yaml:"starred" json:"starred"`
	Important  bool `yaml:"important" json:"important"`
	SentByUser bool `yaml:"sent_by_user" json:"sent_by_user"`
	RecentDays int  `yaml:"recent_days" json:"recent_days"`
}

// DefaultKeep returns the safe-by-default profile referenced in §6.
func DefaultKeep() KeepConfig {
	return KeepConfig{
		Contacts:   true,
		Replied:    true,
		Starred:    true,
		Important:  true,
		SentByUser: true,
		RecentDays: 365,
	}
}

// Protect applies §6 rules. Priority: hard labels first (starred/important/sent),
// then identity rules (contact/replied), then the recent window, then the
// domain whitelist. The first hit wins.
//
// Note on Replied: Gmail exposes per-message reply state via thread metadata,
// not labels. The fixture loader flattens this into a synthetic "REPLIED"
// label and the real client will read thread metadata then synthesize the
// same label on the scan step.
func Protect(m *models.Message, cfg KeepConfig, whitelist []string) ProtectResult {
	// 1. Hard labels win first.
	for _, l := range m.Labels {
		switch l {
		case "STARRED":
			if cfg.Starred {
				return ProtectResult{Protected: true, Reason: models.ReasonStarred}
			}
		case "IMPORTANT":
			if cfg.Important {
				return ProtectResult{Protected: true, Reason: models.ReasonImportant}
			}
		case "SENT":
			if cfg.SentByUser {
				return ProtectResult{Protected: true, Reason: models.ReasonSentByUser}
			}
		}
	}

	// 2. Identity rules — contacts and replied-to beat basic recency.
	if cfg.Replied && hasRepliedLabel(m) {
		return ProtectResult{Protected: true, Reason: models.ReasonReplied}
	}
	if cfg.Contacts && m.Sender.IsContact {
		return ProtectResult{Protected: true, Reason: models.ReasonContact}
	}

	// 3. Recent window.
	if cfg.RecentDays > 0 {
		cutoff := time.Now().Add(-time.Duration(cfg.RecentDays) * 24 * time.Hour)
		if m.Date.After(cutoff) {
			return ProtectResult{Protected: true, Reason: models.ReasonRecent}
		}
	}

	// 4. Domain whitelist (matches sender-domain, not full email).
	if len(whitelist) > 0 {
		domain := extractDomain(m.Sender.Email)
		for _, w := range whitelist {
			if w == "" {
				continue
			}
			if strings.EqualFold(strings.TrimPrefix(w, "@"), domain) {
				return ProtectResult{Protected: true, Reason: models.ReasonWhitelisted}
			}
		}
	}
	return ProtectResult{}
}

func hasRepliedLabel(m *models.Message) bool {
	for _, l := range m.Labels {
		if l == "REPLIED" {
			return true
		}
	}
	return false
}
