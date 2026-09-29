package jobs

import (
	"strings"
	"unicode"
)

// activeTaskReviewReason is a conservative backstop for decisions made in
// active mode. It is intentionally fail-closed for non-Latin mutations because
// the current reviewed activation data is English-only.
func activeTaskReviewReason(task, mode string) string {
	if mode != "implement" && mode != "auto" {
		return ""
	}
	if containsNonLatinLetter(task) {
		return "non_latin_implementation_requires_review"
	}

	words := taskWords(task)
	if !hasMutationVerb(words) {
		return ""
	}

	if containsAny(words, "production", "prod", "live", "customer", "customers", "payment", "payments", "billing", "transaction", "transactions") {
		return "high_risk_operation_detected"
	}
	if containsAny(words, "credential", "credentials", "secret", "secrets", "token", "tokens", "key", "keys", "authentication", "auth", "authorization", "firewall", "tls", "cryptographic", "encryption", "permission", "permissions", "access") &&
		containsAny(words, "revoke", "revokes", "revoked", "rotate", "rotates", "rotated", "invalidate", "invalidates", "invalidated", "disable", "disables", "disabled", "remove", "removes", "removed", "delete", "deletes", "deleting", "drop", "drops", "dropped", "expose", "exposes", "bypass", "bypasses", "weaken", "weakens", "replace", "replaces", "replaced") {
		return "high_risk_operation_detected"
	}
	if containsAny(words, "permanent", "permanently", "irreversible", "irreversibly") &&
		containsAny(words, "database", "databases", "db", "table", "tables", "data", "record", "records", "account", "accounts", "bucket", "buckets", "object", "objects") {
		return "high_risk_operation_detected"
	}
	return ""
}

func hasMutationVerb(words map[string]bool) bool {
	return containsAny(words,
		"delete", "deletes", "deleting", "drop", "drops", "dropped", "dropping", "purge", "purges", "purged", "destroy", "destroys",
		"remove", "removes", "removed", "revoke", "revokes", "revoked", "rotate", "rotates", "rotated", "invalidate", "invalidates",
		"invalidated", "disable", "disables", "disabled", "deploy", "deploys", "deployed", "migrate", "migrates", "migrated", "migration",
		"rewrite", "rewrites", "rewrote", "change", "changes", "changed", "modify", "modifies", "modified", "replace", "replaces", "replaced",
		"publish", "publishes", "published", "release", "releases", "run", "runs", "execute", "executes", "write", "writes", "alter", "alters",
		"update", "updates", "updated", "charge", "charges", "charged", "apply", "applies", "expose", "exposes", "bypass", "bypasses", "weaken", "weakens")
}

func taskWords(task string) map[string]bool {
	words := make(map[string]bool)
	for _, word := range strings.FieldsFunc(strings.ToLower(task), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	}) {
		words[word] = true
	}
	return words
}

func containsAny(words map[string]bool, candidates ...string) bool {
	for _, candidate := range candidates {
		if words[candidate] {
			return true
		}
	}
	return false
}

func containsNonLatinLetter(text string) bool {
	for _, r := range text {
		if unicode.IsLetter(r) && !unicode.In(r, unicode.Latin) {
			return true
		}
	}
	return false
}
