package config

import "strings"

// MaskedPlaceholder is the normative value that replaces secrets in
// diagnostic output (ADR-0001 v2.2 §6). Earlier versions (v2.0/v2.1) used
// "***"; v2.2 unifies on "[MASKED]" as more self-documenting.
const MaskedPlaceholder = "[MASKED]"

// Source of truth for these patterns lives in the spec submodule at
// `spec/_meta/secret_patterns.yaml`. When the spec is updated, sync the
// constants below.

var secretSuffixes = []string{
	"_key",
	"_secret",
	"_token",
	"_password",
	"_passphrase",
	"_credentials",
	"_credential",
	"_auth",
	"_api_key",
	"_access_key",
	"_private_key",
}

var secretPrefixes = []string{
	"api_key",
	"api_token",
	"secret",
	"password",
	"private_key",
	"access_token",
	"bearer",
}

var secretExact = map[string]struct{}{
	"api_key":     {},
	"apikey":      {},
	"password":    {},
	"passwd":      {},
	"pw":          {},
	"token":       {},
	"secret":      {},
	"credentials": {},
}

// IsSecretField reports whether the field name matches the ADR v2.2 §6
// secret-pattern list. Match order: exact → suffix → prefix (OR). Case-
// insensitive.
func IsSecretField(name string) bool {
	lowered := strings.ToLower(name)
	if _, ok := secretExact[lowered]; ok {
		return true
	}
	for _, suffix := range secretSuffixes {
		if strings.HasSuffix(lowered, suffix) {
			return true
		}
	}
	for _, prefix := range secretPrefixes {
		if strings.HasPrefix(lowered, prefix) {
			return true
		}
	}
	return false
}

// MaskValue returns MaskedPlaceholder if name is secret and value is non-empty.
// Empty / nil values pass through (nothing to mask; showing "[MASKED]" for nil
// would be misleading).
func MaskValue(name string, value any) any {
	if !IsSecretField(name) {
		return value
	}
	if value == nil {
		return value
	}
	if s, ok := value.(string); ok && s == "" {
		return value
	}
	return MaskedPlaceholder
}
