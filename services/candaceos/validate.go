package candaceos

import (
	"fmt"
	"sort"
	"strings"
)

func validateID(field, value string) error {
	if value == "" {
		return fmt.Errorf("%s is required", field)
	}
	if value != strings.TrimSpace(value) || strings.ContainsAny(value, " \t\r\n") {
		return fmt.Errorf("%s must not contain whitespace", field)
	}
	return nil
}

func validateLabels(field string, labels map[string]string, requireOne bool) error {
	if requireOne && len(labels) == 0 {
		return fmt.Errorf("%s must contain at least one exact match", field)
	}
	keys := make([]string, 0, len(labels))
	for key := range labels {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		value := labels[key]
		if key == "" || key != strings.TrimSpace(key) || strings.ContainsAny(key, " \t\r\n") {
			return fmt.Errorf("%s contains an invalid key %q", field, key)
		}
		if value == "" || value != strings.TrimSpace(value) {
			return fmt.Errorf("%s[%q] must be a non-empty trimmed value", field, key)
		}
	}
	return nil
}
