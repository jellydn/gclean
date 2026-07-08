package engine

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"gclean/internal/models"
)

// Rule is the parsed form of one entry under delete:/archive:/keep: in the
// config file. The DSL grammar is intentionally minimal:
//
//	<predicate>:<value> [<predicate>:<value> ...]
//
// Supported predicates:
//   - has:<header-name>        — message has the named RFC822 header
//   - category:<name>          — Gmail category (promotions, social, ...)
//   - from:<substring>         — substring match against From address
//   - older_than:<Nd>          — date is older than N days
//   - larger_than:<NB|KB|MB|GB>  — message size exceeds N bytes
//
// Multiple predicates AND together.
type Rule struct {
	Action     string // "delete" | "archive" | "keep"
	Predicates []Predicate
}

// Predicate is one key:value pair in a Rule.
type Predicate struct {
	Key   string
	Value string
}

// ParseRule parses one DSL line. Whitespace or commas separate predicates
// within a rule; commas are tolerated to keep config authoring forgiving.
func ParseRule(action, raw string) (Rule, error) {
	r := Rule{Action: action}
	fields := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ' ' || r == '\t' || r == ','
	})
	for _, f := range fields {
		if f == "" {
			continue
		}
		i := strings.Index(f, ":")
		if i <= 0 || i == len(f)-1 {
			return r, fmt.Errorf("invalid predicate %q (expected key:value)", f)
		}
		r.Predicates = append(r.Predicates, Predicate{Key: f[:i], Value: f[i+1:]})
	}
	return r, nil
}

// Matches returns true if every predicate in the rule is satisfied by m.
// An empty rule never matches.
func (r Rule) Matches(m *models.Message) bool {
	if len(r.Predicates) == 0 {
		return false
	}
	for _, p := range r.Predicates {
		if !matchPredicate(m, p) {
			return false
		}
	}
	return true
}

func matchPredicate(m *models.Message, p Predicate) bool {
	switch p.Key {
	case "has":
		needle := strings.ToLower(p.Value)
		for k := range m.Headers {
			if strings.Contains(strings.ToLower(k), needle) {
				return true
			}
		}
		return false
	case "subject":
		return strings.Contains(strings.ToLower(m.Subject), strings.ToLower(p.Value))
	case "category":
		want := "CATEGORY_" + strings.ToUpper(p.Value)
		for _, l := range m.Labels {
			if l == want {
				return true
			}
		}
		return false
	case "from":
		return strings.Contains(strings.ToLower(m.Sender.Email), strings.ToLower(p.Value))
	case "older_than":
		d, err := ParseDuration(p.Value)
		if err != nil {
			return false
		}
		return time.Since(m.Date) > d
	case "larger_than":
		b, err := ParseByteSize(p.Value)
		if err != nil {
			return false
		}
		return m.Size > b
	default:
		return false
	}
}

// ParseDuration accepts only "Nd" suffix — keeps the grammar tight.
func ParseDuration(s string) (time.Duration, error) {
	if !strings.HasSuffix(s, "d") {
		return 0, fmt.Errorf("duration %q: only 'Nd' suffix supported", s)
	}
	n, err := strconv.Atoi(strings.TrimSuffix(s, "d"))
	if err != nil || n < 0 {
		return 0, fmt.Errorf("duration %q: invalid number of days", s)
	}
	return time.Duration(n) * 24 * time.Hour, nil
}

// ParseByteSize accepts "B" | "KB" | "MB" | "GB" suffix (case-insensitive).
func ParseByteSize(s string) (int64, error) {
	s = strings.ToUpper(strings.TrimSpace(s))
	switch {
	case strings.HasSuffix(s, "GB"):
		n, err := strconv.Atoi(strings.TrimSuffix(s, "GB"))
		if err != nil || n < 0 {
			return 0, fmt.Errorf("size %q: invalid number", s)
		}
		return int64(n) << 30, nil
	case strings.HasSuffix(s, "MB"):
		n, err := strconv.Atoi(strings.TrimSuffix(s, "MB"))
		if err != nil || n < 0 {
			return 0, fmt.Errorf("size %q: invalid number", s)
		}
		return int64(n) << 20, nil
	case strings.HasSuffix(s, "KB"):
		n, err := strconv.Atoi(strings.TrimSuffix(s, "KB"))
		if err != nil || n < 0 {
			return 0, fmt.Errorf("size %q: invalid number", s)
		}
		return int64(n) << 10, nil
	case strings.HasSuffix(s, "B"):
		n, err := strconv.Atoi(strings.TrimSuffix(s, "B"))
		if err != nil || n < 0 {
			return 0, fmt.Errorf("size %q: invalid number", s)
		}
		return int64(n), nil
	}
	return 0, fmt.Errorf("size %q: missing B/KB/MB/GB suffix", s)
}
