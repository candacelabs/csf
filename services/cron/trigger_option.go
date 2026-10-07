// Copyright 2026 Candace Labs

package cron

import (
	"fmt"

	grammar "github.com/candacelabs/csf/pkg/cron"
)

// TriggerOption configures one trigger declared with [WithTrigger].
type TriggerOption func(policies *triggerPolicies) error

// WithCatchUp sets a trigger's missed-occurrence policy.
func WithCatchUp(policy grammar.CatchUpPolicy) TriggerOption {
	return func(policies *triggerPolicies) error {
		if !policy.Valid() {
			return fmt.Errorf("%w: unknown catch-up policy %q", ErrInvalidConfiguration, policy)
		}
		policies.catchUp = policy
		return nil
	}
}

// WithOverlap sets a trigger's concurrent-occurrence policy.
func WithOverlap(policy grammar.OverlapPolicy) TriggerOption {
	return func(policies *triggerPolicies) error {
		if !policy.Valid() {
			return fmt.Errorf("%w: unknown overlap policy %q", ErrInvalidConfiguration, policy)
		}
		policies.overlap = policy
		return nil
	}
}
