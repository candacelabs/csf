// Copyright 2026 Candace Labs

// Package stub is the brain that needs no model: it answers every decision
// with the same canned proposal, so the harness runs and its specs pass with
// no inference provider, network or credential at all.
package stub

import (
	"context"
	"slices"

	"github.com/candacelabs/csf/io/net/model"
)

// ProviderName is the Provider every canned proposal carries.
const ProviderName = "stub"

// CannedBrain proposes the same actions, in the same order, for every
// decision. It is deterministic and immutable after construction, so one value
// is safe to share between goroutines.
type CannedBrain[Context any, Action any] struct {
	actions []Action
}

var _ model.IBrain[struct{}, struct{}] = (*CannedBrain[struct{}, struct{}])(nil)

// NewCannedBrain returns a brain that proposes actions for every decision.
// With no actions it answers model.ErrNoProposal, which is how a spec drives
// a consumer's empty-proposal path.
func NewCannedBrain[Context any, Action any](actions ...Action) *CannedBrain[Context, Action] {
	return &CannedBrain[Context, Action]{actions: slices.Clone(actions)}
}

// Propose returns a fresh copy of the canned actions. It ignores decision by
// design: the stub stands in for reasoning, it does not perform any.
func (brain *CannedBrain[Context, Action]) Propose(ctx context.Context, decision Context) (*model.Proposal[Action], error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(brain.actions) == 0 {
		return nil, model.ErrNoProposal
	}
	return &model.Proposal[Action]{Provider: ProviderName, Actions: slices.Clone(brain.actions)}, nil
}
