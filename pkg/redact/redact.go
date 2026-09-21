// Package redact removes caller-declared sensitive values from text destined
// for logs or operator diagnostics.
//
// A Redactor matches exact, non-empty values and their URL-userinfo-escaped
// spellings. Callers own the policy decision about which values are sensitive;
// this package does not inspect configuration, errors, or process state.
package redact

import (
	"net/url"
	"sort"
	"strings"
)

// Replacement is the stable marker substituted for sensitive text.
const Replacement = "[REDACTED]"

// Redactor is an immutable exact-value replacement policy. The zero value
// leaves text unchanged. A configured Redactor is safe for concurrent use.
type Redactor struct {
	replacer *strings.Replacer
}

// NewRedactor constructs a policy from caller-owned sensitive values. Empty
// values are ignored. Longer and URL-userinfo-escaped forms are matched before
// shorter values so overlapping credentials cannot be partially exposed.
func NewRedactor(secrets ...string) Redactor {
	variants := make(map[string]struct{}, len(secrets)*2)
	for _, secret := range secrets {
		if secret == "" {
			continue
		}
		variants[secret] = struct{}{}
		escaped := strings.TrimPrefix(url.UserPassword("", secret).String(), ":")
		variants[escaped] = struct{}{}
	}
	if len(variants) == 0 {
		return Redactor{}
	}

	ordered := make([]string, 0, len(variants))
	for variant := range variants {
		ordered = append(ordered, variant)
	}
	sort.Slice(ordered, func(left, right int) bool {
		if len(ordered[left]) != len(ordered[right]) {
			return len(ordered[left]) > len(ordered[right])
		}
		return ordered[left] < ordered[right]
	})

	replacements := make([]string, 0, len(ordered)*2)
	for _, variant := range ordered {
		replacements = append(replacements, variant, Replacement)
	}
	return Redactor{replacer: strings.NewReplacer(replacements...)}
}

// String applies the declared redaction policy to text.
func (redactor Redactor) String(text string) string {
	if redactor.replacer == nil {
		return text
	}
	return redactor.replacer.Replace(text)
}
