// Package defang provides runtime assembly of email addresses so that
// Cloudflare's email-obfuscation source-pass (and similar tools) cannot
// rewrite a literal "local@domain" token in source into a placeholder like
// "[email protected]" with no "@". Addresses must be joined at runtime, never
// hard-coded as literals in source.
//
// Keeping this concern in its own module (rather than in the pure engine
// package) means callers import a toolchain-defense helper, not a domain
// module, just to assemble an address.
package defang

// MkEmail joins a local part and a domain at runtime with "@".
//
//	addr := defang.MkEmail("noreply", "example.com") // "noreply@example.com"
func MkEmail(local, domain string) string {
	return local + "@" + domain
}

// DefangSlice joins each [local, domain] pair into an email address.
func DefangSlice(pairs [][2]string) []string {
	out := make([]string, 0, len(pairs))
	for _, p := range pairs {
		out = append(out, MkEmail(p[0], p[1]))
	}
	return out
}

// DefangMap joins each [local, domain] pair into an email address keyed by
// local.
func DefangMap(pairs [][2]string) map[string]string {
	out := make(map[string]string, len(pairs))
	for _, p := range pairs {
		out[p[0]] = MkEmail(p[0], p[1])
	}
	return out
}
