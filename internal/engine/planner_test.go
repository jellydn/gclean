package engine

import (
	"testing"
	"time"

	"gclean/internal/models"
)

// MkEmail() is exported from internal/engine/testutil.go. Same package.

// classified is the test helper for constructing *models.Classified values.
func classified(id, sender, subject string, date time.Time, size int64, isJunk bool, reason string, labels ...string) *models.Classified {
	return &models.Classified{
		Message: &models.Message{
			ID: id, ThreadID: "t", Subject: subject, Size: size, Date: date,
			Sender:  models.Sender{Email: sender},
			Headers: map[string]string{},
			Labels:  labels,
		},
		IsJunk: isJunk, ReasonCode: reason,
	}
}

// findByID looks up the Decision whose Message.ID matches `id`. Asserts via
// t.Fatalf that exactly one such Decision exists, so:
//
//   - A missing decision fails the test loudly instead of returning a zero
//     value that downstream assertions would silently screw against.
//   - A duplicated ID fails the test (e.g. if Plan() ever changes to emit one
//     decision per matching rule rather than per message).
//
// Using this helper instead of decisions[N] keeps tests robust against
// Plan()'s internal sort/regroup behavior — most recently the trailing
// sort.SliceStable by descending size swapped positions and broke positional
// assertions silently.
func findByID(t *testing.T, decisions []models.Decision, id string) models.Decision {
	t.Helper()
	var match models.Decision
	count := 0
	for _, d := range decisions {
		if d.Message.ID == id {
			match = d
			count++
		}
	}
	if count == 0 {
		t.Fatalf("findByID: no decision found with Message.ID=%q (have %d)", id, len(decisions))
	}
	if count > 1 {
		t.Fatalf("findByID: %d decisions share Message.ID=%q (expected exactly 1)", count, id)
	}
	return match
}

func TestPlan_DeleteRule_AppliesOnlyToJunk(t *testing.T) {
	// §15 safety: even if a delete rule matches a non-junk message,
	// the planner must NOT delete it.
	deleteRule, err := ParseRule("delete", "from:example.com")
	if err != nil {
		t.Fatal(err)
	}
	in := PlanInputs{
		Messages: []*models.Classified{
			// Sizes here are illustrative — findByID makes the assertions
			// insensitive to whatever ordering Plan() picks.
			classified("junk", MkEmail("junky-local", "example.com"), "spam", time.Now().Add(-200*24*time.Hour), 1000, true, models.ReasonNoreply),
			classified("human", MkEmail("human-local", "example.com"), "hi", time.Now().Add(-200*24*time.Hour), 2000, false, "", "INBOX"),
		},
		Config: RuleConfig{Delete: []Rule{deleteRule}},
		Keep:   KeepConfig{},
	}
	decisions, rep := Plan(in)

	dJunk := findByID(t, decisions, "junk")
	if dJunk.Verdict != models.VerdictDelete {
		t.Errorf("junk mail should be deleted, got %s reasons=%v", dJunk.Verdict, dJunk.Reasons)
	}
	dHuman := findByID(t, decisions, "human")
	if dHuman.Verdict != models.VerdictKeep {
		t.Errorf("NON-junk mail with matching delete rule MUST be kept (PRD§15), got %s reasons=%v", dHuman.Verdict, dHuman.Reasons)
	}
	if rep.DeleteCount != 1 || rep.KeepCount != 1 {
		t.Errorf("rep counts off: %+v", rep)
	}
}

func TestPlan_KeepRulesBeatsDeleteRules(t *testing.T) {
	// A keep-rule match beats a delete-rule match.
	keepRule, _ := ParseRule("keep", "subject:hello")
	delRule, _ := ParseRule("delete", "from:example.com")
	m := classified("keep-wins", MkEmail("any-local", "example.com"), "hello there", time.Now().Add(-400*24*time.Hour), 100, true, models.ReasonNoreply)
	decisions, _ := Plan(PlanInputs{
		Messages: []*models.Classified{m},
		Config:   RuleConfig{Keep: []Rule{keepRule}, Delete: []Rule{delRule}},
		Keep:     KeepConfig{},
	})
	d := findByID(t, decisions, "keep-wins")
	if d.Verdict != models.VerdictKeep {
		t.Errorf("keep rule should win over delete, got %s", d.Verdict)
	}
}

func TestPlan_IgnoresDomain(t *testing.T) {
	m := classified("ignored-bank", MkEmail("alerts", "bank.com"), "Trusted email", time.Now().Add(-800*24*24*time.Hour), 1000, true, models.ReasonNewsletter)
	decisions, rep := Plan(PlanInputs{
		Messages: []*models.Classified{m},
		Config:   RuleConfig{Ignore: []string{"bank.com"}, Delete: []Rule{}}, // would otherwise match delete-rule
		Keep:     KeepConfig{},
	})
	d := findByID(t, decisions, "ignored-bank")
	if d.Verdict != models.VerdictProtected {
		t.Errorf("ignored domain must be protected (kept out of any action), got %s", d.Verdict)
	}
	if rep.KeepCount != 1 || rep.DeleteCount != 0 {
		t.Errorf("rep: %+v", rep)
	}
}

func TestPlan_ProtectionBeatsRules(t *testing.T) {
	m := classified("starred-msg", MkEmail("boss", "example.com"), "urgent", time.Now().Add(-5*24*time.Hour), 1000, false, "", "STARRED")
	decisions, _ := Plan(PlanInputs{
		Messages: []*models.Classified{m},
		Config:   RuleConfig{Delete: []Rule{{Action: "delete", Predicates: []Predicate{{Key: "from", Value: "any-local"}}}}},
		Keep:     KeepConfig{Starred: true},
	})
	d := findByID(t, decisions, "starred-msg")
	if d.Verdict != models.VerdictProtected {
		t.Errorf("starred must win over delete rule, got %s", d.Verdict)
	}
}

func TestPlan_Archive(t *testing.T) {
	archiveRule, _ := ParseRule("archive", "subject:receipt")
	m := classified("archive-me", MkEmail("stripe-billing", "example.com"), "Receipt for order #1", time.Now().Add(-180*24*time.Hour), 100, true, models.ReasonStripe)
	decisions, rep := Plan(PlanInputs{
		Messages: []*models.Classified{m},
		Config:   RuleConfig{Archive: []Rule{archiveRule}},
		Keep:     KeepConfig{},
	})
	d := findByID(t, decisions, "archive-me")
	if d.Verdict != models.VerdictArchive {
		t.Errorf("archive rule should produce VerdictArchive, got %s", d.Verdict)
	}
	if rep.ArchiveCount != 1 {
		t.Errorf("rep: %+v", rep)
	}
}
