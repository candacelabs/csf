package candaceos

import (
	"fmt"
	"path"
	"regexp"
	"strings"
)

var (
	gitObjectIDPattern = regexp.MustCompile(`^(?:[0-9a-f]{40}|[0-9a-f]{64})$`)
	sha256Pattern      = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
)

// AppRevision is a content-addressed application source snapshot. Revision is
// a full Git object ID rather than a mutable branch or tag, and Digest covers
// the materialized source used for the deployment.
type AppRevision struct {
	ID          string `json:"id" yaml:"id"`
	AppID       string `json:"app_id" yaml:"app_id"`
	Source      string `json:"source" yaml:"source"`
	Revision    string `json:"revision" yaml:"revision"`
	Digest      string `json:"digest" yaml:"digest"`
	ComposePath string `json:"compose_path" yaml:"compose_path"`
}

// Validate ensures the revision names immutable source and a safe,
// repository-relative Compose file.
func (revision AppRevision) Validate() error {
	if err := validateID("id", revision.ID); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidAppRevision, err)
	}
	if err := validateID("app_id", revision.AppID); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidAppRevision, err)
	}
	if revision.Source == "" || revision.Source != strings.TrimSpace(revision.Source) {
		return fmt.Errorf("%w: source must be a non-empty trimmed repository reference", ErrInvalidAppRevision)
	}
	if !gitObjectIDPattern.MatchString(revision.Revision) {
		return fmt.Errorf("%w: revision must be a full lowercase 40- or 64-character Git object ID", ErrInvalidAppRevision)
	}
	if !sha256Pattern.MatchString(revision.Digest) {
		return fmt.Errorf("%w: digest must use canonical sha256:<64 lowercase hex> form", ErrInvalidAppRevision)
	}
	if err := validateComposePath(revision.ComposePath); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidAppRevision, err)
	}
	return nil
}

func validateComposePath(composePath string) error {
	if composePath == "" {
		return fmt.Errorf("compose_path is required")
	}
	if strings.Contains(composePath, `\`) || path.IsAbs(composePath) || path.Clean(composePath) != composePath || strings.HasPrefix(composePath, "../") {
		return fmt.Errorf("compose_path must be a clean repository-relative Linux path")
	}
	ext := strings.ToLower(path.Ext(composePath))
	if ext != ".yaml" && ext != ".yml" {
		return fmt.Errorf("compose_path must name a .yaml or .yml file")
	}
	return nil
}
