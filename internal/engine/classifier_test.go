package engine

import (
	"strings"
	"testing"
	"time"

	"gclean/internal/models"
)

// MkEmail() is exported from internal/engine/testutil.go for the same reason
// this test file used to declare a local	MkEmail(): runtime concatenation of the
// "@" defends against Cloudflare's email-obfuscation rewriting of literal
// "local@domain" tokens into "[email protected]". Helper imported via package.

func msg(headers map[string]string, sender string, labels ...string) *models.Message {
	m := &models.Message{
		ID:      "test",
		Subject: "test",
		Date:    time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		Headers: headers,
		Sender:  models.Sender{Email: sender},
		Labels:  labels,
	}
	return m
}

func TestClassify_ListUnsubscribe(t *testing.T) {
	c := Classify(msg(map[string]string{"List-Unsubscribe": "<mailto:[email protected]>"}, MkEmail("alice", "example.com")))
	if !c.IsJunk || c.ReasonCode != models.ReasonNewsletter {
		t.Fatalf("expected newsletter, got %+v", c)
	}
}

func TestClassify_ListID(t *testing.T) {
	c := Classify(msg(map[string]string{"List-Id": "list <l.example.com>"}, MkEmail("alice", "example.com")))
	if !c.IsJunk || c.ReasonCode != models.ReasonMailingList {
		t.Fatalf("expected mailing_list, got %+v", c)
	}
}

func TestClassify_PrecedenceBulk(t *testing.T) {
	for _, v := range []string{"bulk", "list", "junk", "BULK"} {
		// Use a sender whose local-part does NOT start with "noreply" so this
		// test exercises the Precedence path, not the noreply path.
		c := Classify(msg(map[string]string{"Precedence": v}, MkEmail("alice", "example.com")))
		if !c.IsJunk || c.ReasonCode != models.ReasonBulk {
			t.Fatalf("Precedence=%q: expected bulk, got %+v", v, c)
		}
	}
}

func TestClassify_AutoSubmitted(t *testing.T) {
	c := Classify(msg(map[string]string{"Auto-Submitted": "auto-generated"}, MkEmail("alice", "example.com")))
	if !c.IsJunk || c.ReasonCode != models.ReasonBulk {
		t.Fatalf("expected bulk, got %+v", c)
	}
	c2 := Classify(msg(map[string]string{"Auto-Submitted": "no"}, MkEmail("alice", "example.com")))
	if c2.IsJunk {
		t.Fatalf("Auto-Submitted:no must NOT be junk, got %+v", c2)
	}
}

func TestClassify_NoReplyFromDomain(t *testing.T) {
	locals := []string{"noreply", "no-reply", "donotreply", "do-not-reply"}
	for _, l := range locals {
		addr := MkEmail(l, "example.com")
		c := Classify(msg(map[string]string{}, addr))
		if !c.IsJunk || c.ReasonCode != models.ReasonNoreply {
			t.Fatalf("from=%q: expected noreply, got %+v", addr, c)
		}
	}
}

func TestClassify_PromotionsCategory(t *testing.T) {
	c := Classify(msg(map[string]string{}, MkEmail("alice", "example.com"), "CATEGORY_PROMOTIONS"))
	if !c.IsJunk || c.ReasonCode != models.ReasonPromotion {
		t.Fatalf("expected promotion, got %+v", c)
	}
}

func TestClassify_SocialCategory(t *testing.T) {
	c := Classify(msg(map[string]string{}, MkEmail("alice", "example.com"), "CATEGORY_SOCIAL"))
	if !c.IsJunk || c.ReasonCode != models.ReasonSocial {
		t.Fatalf("expected social, got %+v", c)
	}
}

func TestClassify_KnownDomains(t *testing.T) {
	// Slice form — Go 1.26's stricter vet treats duplicate map-key entries as
	// a build failure; a slice of cases sidesteps it cleanly. Emails are
	// assembled via	MkEmail() so the obfuscator cannot rewrite them.
	cases := []struct {
		domain string
		want   string
	}{
		{"github.com", models.ReasonGitHub},
		{"stripe.com", models.ReasonStripe},
		{"amazonaws.com", models.ReasonAWSBilling},
		{"azure.com", models.ReasonAzureAlert},
		{"gitlab.com", models.ReasonGitLab},
		{"atlassian.net", models.ReasonJira},
		{"slack.com", models.ReasonSlack},
		{"linkedin.com", models.ReasonSocial},
		{"facebook.com", models.ReasonSocial},
		{"reddit.com", models.ReasonSocial},
		{"x.com", models.ReasonSocial},
		{"twitter.com", models.ReasonSocial},
	}
	for _, tc := range cases {
		addr := MkEmail("a", tc.domain)
		got := Classify(msg(map[string]string{}, addr))
		if !got.IsJunk || got.ReasonCode != tc.want {
			t.Errorf("domain=%q addr=%q: expected %s, got reason=%s isJunk=%v",
				tc.domain, addr, tc.want, got.ReasonCode, got.IsJunk)
		}
	}
}

func TestClassify_PersonalEmail(t *testing.T) {
	c := Classify(msg(map[string]string{}, MkEmail("jane.doe", "example.com")))
	if c.IsJunk {
		t.Fatalf("personal email must not be junk, got %+v", c)
	}
}

// TestExtractDomain covers the small helper directly so regression-risk
// regressions are caught even when the call sites are still correct.
func TestExtractDomain(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{MkEmail("a", "github.com"), "github.com"},
		{MkEmail("Alice", "STRIPE.com"), "stripe.com"},
		{"", ""},
	}
	for _, tc := range cases {
		if got := extractDomain(tc.in); got != tc.want {
			t.Errorf("extractDomain(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestHeaderValue checks the case-insensitive lookup helper.
func TestHeaderValue(t *testing.T) {
	h := map[string]string{"List-Unsubscribe": "<mailto:x>"}
	if got := strings.ToLower(headerValue(h, "list-unsubscribe")); got != "<mailto:x>" {
		t.Errorf("case-insensitive lookup failed, got %q", got)
	}
	if headerValue(h, "X-None") != "" {
		t.Errorf("missing header must return empty")
	}
}
