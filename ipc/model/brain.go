// Copyright 2026 Candace Labs

// Package model is CSF's contract with a brain: an agent loop over a large
// model behind an inference provider or API. The brain is external; CSF ships
// this contract and a deterministic stub (ipc/model/stub), and each provider
// lives in its own subpackage, ipc/model/<provider>, because reaching a model
// crosses a process or network boundary.
//
// Given context, a brain proposes actions. A language model's proposal is not
// permission to execute it: admission, checks and execution belong to the
// caller.
package model

import (
	"context"
	"errors"
	"fmt"
)

// ErrNoProposal reports that a brain answered without proposing any action.
var ErrNoProposal = errors.New("brain proposed no action")

// IBrain is an agent loop over a large model behind an inference provider.
// Context is the information supplied for one decision; Action is what the
// brain may propose. Both are type parameters, so a caller never holds an
// untyped value at this boundary.
type IBrain[Context any, Action any] interface {
	// Propose returns the actions the brain proposes for decision. A proposal
	// is never permission to execute it.
	Propose(ctx context.Context, decision Context) (*Proposal[Action], error)
}

// Proposal is a brain's answer for one decision. It is data, not an
// authorization: the caller decides whether any action is admitted.
type Proposal[Action any] struct {
	// Provider names the inference provider that produced the proposal.
	Provider string
	// Actions are the proposed actions in the order the brain gave them.
	Actions []Action
}

// Only returns the single action of a proposal that must contain exactly one.
func (proposal *Proposal[Action]) Only() (Action, error) {
	var zero Action
	if proposal == nil || len(proposal.Actions) == 0 {
		return zero, ErrNoProposal
	}
	if len(proposal.Actions) != 1 {
		return zero, &AmbiguousProposalError{Provider: proposal.Provider, Count: len(proposal.Actions)}
	}
	return proposal.Actions[0], nil
}

// AmbiguousProposalError reports more proposed actions than the caller can
// admit for one decision.
type AmbiguousProposalError struct {
	Provider string
	Count    int
}

func (failure *AmbiguousProposalError) Error() string {
	return fmt.Sprintf("brain %q proposed %d actions where one was expected", failure.Provider, failure.Count)
}
