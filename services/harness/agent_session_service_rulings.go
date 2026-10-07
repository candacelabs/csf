// Copyright 2026 Candace Labs

package harness

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/candacelabs/csf/csf"
	harnessv1 "github.com/candacelabs/csf/proto/candace/harness/v1"
	"github.com/candacelabs/csf/services/harness/session"
)

// RecordRuling records one operator ruling under the harness's state
// directory and returns the rulings in force. Every turn sent afterwards, in
// every session, carries them to the question gate; the Workbench, the
// rulings view and the rulings series read the same records.
func (service *AgentSessionService) RecordRuling(ctx context.Context, request *harnessv1.RecordRulingRequest) (*harnessv1.RecordRulingResponse, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := harnessv1.ValidateRecordRulingRequest(request); err != nil {
		return nil, fmt.Errorf("%w: %w", csf.ErrInvalidRequest, err)
	}
	ruling := request.GetRuling()
	switch {
	case ruling == nil:
		return nil, fmt.Errorf("%w: a ruling is required", csf.ErrInvalidRequest)
	case slices.ContainsFunc(ruling.GetExcludes(), func(phrase string) bool { return strings.TrimSpace(phrase) == "" }):
		return nil, fmt.Errorf("%w: an excluded alternative is blank", csf.ErrInvalidRequest)
	}
	state := service.runner.StateDirectory()
	if err := session.AppendRuling(state, session.Ruling{
		ID:          ruling.GetRulingId(),
		Statement:   ruling.GetStatement(),
		Excludes:    ruling.GetExcludes(),
		Supersedes:  ruling.GetSupersedes(),
		Quote:       ruling.GetQuote(),
		RuledOn:     ruling.GetRuledOn(),
		Scope:       ruling.GetScope(),
		Why:         ruling.GetWhy(),
		EnforcedBy:  ruling.GetEnforcedBy(),
		PendingGate: ruling.GetPendingGate(),
		RecordedAt:  service.clock.Now(),
	}); err != nil {
		return nil, err
	}
	inForce, err := session.RulingsInForce(state)
	if err != nil {
		return nil, err
	}
	response := &harnessv1.RecordRulingResponse{InForce: make([]*harnessv1.Ruling, 0, len(inForce))}
	for _, one := range inForce {
		response.InForce = append(response.InForce, &harnessv1.Ruling{
			RulingId: one.ID, Statement: one.Statement, Excludes: one.Excludes, Supersedes: one.Supersedes,
			Quote: one.Quote, RuledOn: one.RuledOn, Scope: one.Scope, Why: one.Why,
			EnforcedBy: one.EnforcedBy, PendingGate: one.PendingGate,
		})
	}
	return response, nil
}
