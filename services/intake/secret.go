// Copyright 2026 Candace Labs

package intake

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/candacelabs/csf/pkg/privatefile"
)

const maxProvidersBytes = 1 << 20

// githubProviders is the part of providers.json the receiver reads; the
// other keys belong to other readers.
type githubProviders struct {
	GitHub struct {
		WebhookSecret string `json:"webhook_secret"`
	} `json:"github"`
}

// ReadWebhookSecret reads github.webhook_secret from the providers.json at
// path, which must be readable by its owner only. No file, or no key, is
// [ErrNoSecret].
func ReadWebhookSecret(path string) ([]byte, error) {
	name := filepath.Base(path)
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("%w: %s does not exist", ErrNoSecret, name)
	}
	content, err := privatefile.Read(path, maxProvidersBytes)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	var providers githubProviders
	if err := json.Unmarshal(content, &providers); err != nil {
		return nil, fmt.Errorf("decode %s: %w", name, err)
	}
	if providers.GitHub.WebhookSecret == "" {
		return nil, fmt.Errorf("%w: %s has no github.webhook_secret", ErrNoSecret, name)
	}
	return []byte(providers.GitHub.WebhookSecret), nil
}
