package engine

import (
	"sort"
	"strings"

	"gclean/internal/models"
)

// PlanInputs bundles everything the planner needs.
type PlanInputs struct {
	Messages []*models.Classified
	Config   RuleConfig
	Keep     KeepConfig
}

// RuleConfig is the parsed, evaluator-ready form of the config file under
// delete:/archive:/keep:/ignore:.
type RuleConfig struct {
	Keep    []Rule
	Delete  []Rule
	Archive []Rule
	Ignore  []string
}

// DomainIgnored reports whether the sender's domain matches the ignore list.
func (rc *RuleConfig) DomainIgnored(domain string) bool {
	for _, d := range rc.Ignore {
		if d == "" {
			continue
		}
		if strings.EqualFold(strings.TrimPrefix(d, "@"), domain) {
			return true
		}
	}
	return false
}

// Plan computes a Decision per message. Per-message order of operations:
//
//  1. Domain in ignore list → VerdictProtected ("ignored_domain")
//  2. Protect() wins → VerdictProtected with reason
//  3. Config.Keep rule match → VerdictKeep
//  4. Config.Archive rule match → VerdictArchive
//  5. Config.Delete rule match → VerdictDelete, but ONLY if the message is
//     classified as junk (PRD §15 "safe by default": refuse to delete
//     non-junk even when a delete rule matches)
//  6. Default → VerdictKeep
//
// This is the safety-critical seam: any logical change here is the only
// thing that determines what gets moved to Trash.
func Plan(in PlanInputs) ([]models.Decision, models.DryRunReport) {
	decisions := make([]models.Decision, 0, len(in.Messages))
	rep := models.DryRunReport{
		DeleteBySender:  map[string]int64{},
		RecoverByReason: map[string]int64{},
	}

	for _, c := range in.Messages {
		m := c.Message
		d := models.Decision{Message: m, Classified: c}

		// 1. Domain ignored outright.
		if in.Config.DomainIgnored(extractDomain(m.Sender.Email)) {
			d.Verdict = models.VerdictProtected
			d.Reasons = append(d.Reasons, "ignored_domain")
			decisions = append(decisions, d)
			rep.KeepCount++
			continue
		}

		// 2. Protect() wins — protects, doesn't delete.
		if pr := Protect(m, in.Keep, []string{}); pr.Protected {
			d.Verdict = models.VerdictProtected
			d.Reasons = append(d.Reasons, "protect:"+pr.Reason)
			decisions = append(decisions, d)
			rep.KeepCount++
			continue
		}

		// 3. Config.Keep rule match.
		if matched, name := matchAny(in.Config.Keep, m); matched {
			d.Verdict = models.VerdictKeep
			d.Reasons = append(d.Reasons, "config_keep:"+name)
			decisions = append(decisions, d)
			rep.KeepCount++
			continue
		}

		// 4. Config.Archive rule match.
		if matched, name := matchAny(in.Config.Archive, m); matched {
			d.Verdict = models.VerdictArchive
			d.Reasons = append(d.Reasons, "config_archive:"+name)
			decisions = append(decisions, d)
			rep.ArchiveCount++
			continue
		}

		// 5. Config.Delete rule match — but refuse if not junk.
		if matched, name := matchAny(in.Config.Delete, m); matched {
			if !c.IsJunk {
				d.Verdict = models.VerdictKeep
				d.Reasons = append(d.Reasons, "delete_rule_refused_non_junk")
				decisions = append(decisions, d)
				rep.KeepCount++
				continue
			}
			d.Verdict = models.VerdictDelete
			d.Reasons = append(d.Reasons, "config_delete:"+name)
			decisions = append(decisions, d)
			rep.DeleteCount++
			rep.RecoverBytes += m.Size
			rep.DeleteBySender[m.Sender.Email]++
			rep.RecoverByReason[c.ReasonCode]++
			if len(rep.SampleDeletes) < 10 {
				rep.SampleDeletes = append(rep.SampleDeletes, m.ID+": "+m.Subject)
			}
			continue
		}

		// 6. Default keep.
		d.Verdict = models.VerdictKeep
		d.Reasons = append(d.Reasons, "default_keep")
		decisions = append(decisions, d)
		rep.KeepCount++
	}

	// Surface largest-impact delete candidates first.
	sort.SliceStable(decisions, func(i, j int) bool {
		return decisions[i].Message.Size > decisions[j].Message.Size
	})
	return decisions, rep
}

// matchAny returns the first matching rule (and a short, reportable name).
func matchAny(rs []Rule, m *models.Message) (bool, string) {
	for _, r := range rs {
		if r.Matches(m) {
			return true, shortRuleName(r)
		}
	}
	return false, ""
}

func shortRuleName(r Rule) string {
	if len(r.Predicates) == 0 {
		return r.Action
	}
	p := r.Predicates[0]
	return p.Key + ":" + p.Value
}
