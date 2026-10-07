// Copyright 2026 Candace Labs

package github

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

// GhHostsFile is the gh CLI's account file, under its config directory.
const GhHostsFile = "hosts.yml"

// ghHost is the part of a hosts.yml entry read here: the active account's
// token, which gh writes when it stores the token in the file rather than a
// keyring.
type ghHost struct {
	OAuthToken string `yaml:"oauth_token"`
}

// TokenFromGhHosts is github.com's active-account token in the gh CLI's
// hosts.yml: the credential gh itself already uses, so CSF holds no second
// one. ErrNoToken when the file names none (gh keeps it in a keyring).
func TokenFromGhHosts(content []byte) (string, error) {
	var hosts map[string]ghHost
	if err := yaml.Unmarshal(content, &hosts); err != nil {
		return "", fmt.Errorf("github: read gh's %s: %w", GhHostsFile, err)
	}
	token := hosts[host].OAuthToken
	if token == "" {
		return "", ErrNoToken
	}
	return token, nil
}

const host = "github.com"
