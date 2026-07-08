// Package engine contains gclean's safety-critical decision logic. Everything
// in this package must be deterministic and pure — no I/O, no clocks beyond
// what's passed in — so it can be unit-tested against fixtures.
package engine

import (
	"net/mail"
	"strings"

	"gclean/internal/models"
)

// Classify runs all junk signals against a message and returns the strongest
// matching reason. Order reflects the strongest signal first: a "noreply"
// local part explicitly tells us "this sender will never reply" — a stronger
// behavioural signal than the vendor domain it happens to come from — so we
// tag `noreply@github.com` as noreply, not github_notification. After that,
// vendor-domain matches (Stripe, GitHub, …) outrank generic header signals
// because the user thinks "GitHub notifications" as a category. Last, Gmail's
// own labels are the weakest signal because the user can't override them.
//
//  1. From-local noreply prefix match (strongest behavioural signal).
//  2. Known vendor domains (Stripe, GitHub, AWS, Azure, GitLab, Jira,
//     Slack, social domains) — most specific vendor reasoning.
//  3. RFC822 header signals (List-Unsubscribe, List-ID,
//     Precedence: bulk|list|junk, Auto-Submitted). Case-insensitive.
//  4. Gmail categories (CATEGORY_PROMOTIONS, _SOCIAL, _UPDATES, _FORUMS).
func Classify(m *models.Message) models.Classified {
	c := models.Classified{Message: m}
	sender := strings.ToLower(m.Sender.Email)

	// 1. From-local noreply prefix — checked BEFORE known vendor domains
	// so that "noreply@github.com" is tagged as noreply, not github_notification.
	if isNoReply(sender) {
		c.IsJunk = true
		c.ReasonCode = models.ReasonNoreply
		return c
	}

	// 2. Known vendor domains.
	if isJunk, code := classifyKnownDomain(sender); isJunk {
		c.IsJunk = true
		c.ReasonCode = code
		return c
	}

	// 3. Header-based bulk signals — generic newsletter / mailing list.
	if headerValue(m.Headers, "List-Unsubscribe") != "" {
		c.IsJunk = true
		c.ReasonCode = models.ReasonNewsletter
		return c
	}
	if headerValue(m.Headers, "List-ID") != "" {
		c.IsJunk = true
		c.ReasonCode = models.ReasonMailingList
		return c
	}
	if v := headerValue(m.Headers, "Precedence"); isBulkPrecedence(v) {
		c.IsJunk = true
		c.ReasonCode = models.ReasonBulk
		return c
	}
	if v := headerValue(m.Headers, "Auto-Submitted"); v != "" && !strings.EqualFold(v, "no") {
		c.IsJunk = true
		c.ReasonCode = models.ReasonBulk
		return c
	}

	// 4. Gmail category — last, generic.
	for _, l := range m.Labels {
		switch l {
		case "CATEGORY_PROMOTIONS":
			c.IsJunk = true
			c.ReasonCode = models.ReasonPromotion
			return c
		case "CATEGORY_SOCIAL":
			c.IsJunk = true
			c.ReasonCode = models.ReasonSocial
			return c
		case "CATEGORY_UPDATES":
			c.IsJunk = true
			c.ReasonCode = models.ReasonNewsletter
			return c
		case "CATEGORY_FORUMS":
			c.IsJunk = true
			c.ReasonCode = models.ReasonMailingList
			return c
		}
	}
	return c
}

func isBulkPrecedence(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "bulk", "list", "junk":
		return true
	}
	return false
}

// isNoReply matches on the local part of the address, prefix-style.
// Substring matching was too greedy (a person named "noreply@example.com"
// would falsely match); prefix matching aligns with real-world noreply
// patterns where the local part IS the entire trigger.
func isNoReply(email string) bool {
	local := email
	if i := strings.Index(local, "@"); i >= 0 {
		local = local[:i]
	}
	local = strings.ToLower(local)
	for _, prefix := range []string{"noreply", "no-reply", "donotreply", "do-not-reply"} {
		if strings.HasPrefix(local, prefix) {
			return true
		}
	}
	return false
}

func classifyKnownDomain(email string) (bool, string) {
	domain := extractDomain(email)
	switch domain {
	case "github.com", "notifications.github.com":
		return true, models.ReasonGitHub
	case "stripe.com":
		return true, models.ReasonStripe
	case "amazonaws.com", "aws.amazon.com":
		return true, models.ReasonAWSBilling
	case "azure.com", "microsoft.com":
		return true, models.ReasonAzureAlert
	case "gitlab.com":
		return true, models.ReasonGitLab
	case "atlassian.net", "jira.com":
		return true, models.ReasonJira
	case "slack.com":
		return true, models.ReasonSlack
	case "linkedin.com", "facebook.com", "reddit.com", "x.com", "twitter.com":
		return true, models.ReasonSocial
	}
	return false, ""
}

// headerValue does a case-insensitive lookup over RFC822-style header keys.
// Map keys in fixtures are case-sensitive in Go, but RFC headers are not.
func headerValue(h map[string]string, name string) string {
	for k, v := range h {
		if strings.EqualFold(k, name) {
			return v
		}
	}
	return ""
}

func extractDomain(addr string) string {
	if addr == "" {
		return ""
	}
	a, err := mail.ParseAddress(addr)
	domain := ""
	if err == nil {
		if i := strings.LastIndex(a.Address, "@"); i >= 0 && i < len(a.Address)-1 {
			domain = a.Address[i+1:]
		}
	}
	if domain == "" {
		if i := strings.LastIndex(addr, "@"); i >= 0 && i < len(addr)-1 {
			domain = addr[i+1:]
		}
	}
	return strings.ToLower(strings.TrimSpace(domain))
}
