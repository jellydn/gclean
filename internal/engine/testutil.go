package engine

// MkEmail joins a local part and a domain at runtime with "@". The reason
// this exists is not stylistic — it's a defense against Cloudflare's email
// obfuscation (and similar source-pass tools) silently rewriting any literal
// "local@domain" token in source into a placeholder like "[email protected]"
// with no "@". Go test fixtures, demo commands, and CLI examples all benefit
// from going through this helper rather than hard-coding addresses.
//
// Usage:
//
//	addr := MkEmail("noreply", "example.com") // "noreply@example.com"
//
// Keep MkEmail in non-test code so production loaders (fixture JSON parsers,
// sample-data generators) can also dodge the same attack, not just unit tests.
func MkEmail(local, domain string) string {
	return local + "@" + domain
}
