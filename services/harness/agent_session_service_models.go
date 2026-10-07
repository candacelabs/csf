// Copyright 2026 Candace Labs

package harness

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/candacelabs/csf/csf"
	"github.com/candacelabs/csf/pkg/atomicfile"
)

// AllowedModelsFile is this host's model policy, under the state directory:
// the models a real session may run on, each with the ruling that allows it.
// It is data the operator edits; every check reads it again, so an edit takes
// effect without a restart.
const AllowedModelsFile = "allowed-models.json"

var (
	// ErrModelNotAllowed reports a session asked to run on a model the host's
	// policy does not allow.
	ErrModelNotAllowed = fmt.Errorf("%w: the model is not allowed on this host", csf.ErrInvalidRequest)
	// ErrNoAllowedModels reports a policy that allows no model at all, so no
	// session may run.
	ErrNoAllowedModels = fmt.Errorf("%w: the host's model policy allows no model", csf.ErrInvalidRequest)
	// ErrModelPolicy reports a policy file that cannot be read as one.
	ErrModelPolicy = errors.New("harness: the model policy cannot be read")
)

// AllowedModel is one model a real session may run on and the ruling that
// allows it.
type AllowedModel struct {
	Model  string `json:"model"`
	Ruling string `json:"ruling"`
	// RuledBy and RuledOn are who ruled and the day, as YYYY-MM-DD.
	RuledBy string `json:"ruled_by"`
	RuledOn string `json:"ruled_on"`
}

// ModelPolicy is the allowed-models file.
type ModelPolicy struct {
	Allowed []AllowedModel `json:"allowed"`
}

// DefaultModelPolicy is the policy a host starts from when its state
// directory records none: the operator's ruling of 2026-10-05.
var DefaultModelPolicy = ModelPolicy{Allowed: []AllowedModel{{
	Model:   "claude-opus-5-5",
	Ruling:  "every real session runs claude-opus-5-5. Never Fable.",
	RuledBy: "operator",
	RuledOn: "2026-10-05",
}}}

// Admit refuses model unless the policy allows it. The refusal names the
// allowed models and the rulings that allow them.
func (policy ModelPolicy) Admit(model string) error {
	if len(policy.Allowed) == 0 {
		return ErrNoAllowedModels
	}
	allowed := make([]string, 0, len(policy.Allowed))
	for _, entry := range policy.Allowed {
		if entry.Model == model {
			return nil
		}
		allowed = append(allowed, fmt.Sprintf("%s (%s, %s: %q)", entry.Model, entry.RuledBy, entry.RuledOn, entry.Ruling))
	}
	return fmt.Errorf("%w: %q; allowed: %s", ErrModelNotAllowed, model, strings.Join(allowed, "; "))
}

func (service *AgentSessionService) modelPolicyPath() string {
	return filepath.Join(service.runner.StateDirectory(), AllowedModelsFile)
}

// RecordModelPolicy replaces the model policy recorded under the state
// directory.
func RecordModelPolicy(stateDirectory string, policy ModelPolicy) error {
	content, err := json.MarshalIndent(policy, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(stateDirectory, AllowedModelsFile)
	if err := atomicfile.WriteFile(path, append(content, '\n'), stateFileMode); err != nil {
		return fmt.Errorf("harness: record the model policy: %w", err)
	}
	return nil
}

// seedModelPolicy records the default policy when the state directory has
// none, so the policy is data the operator can read and edit.
func (service *AgentSessionService) seedModelPolicy() error {
	if _, err := os.Stat(service.modelPolicyPath()); err == nil || !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return RecordModelPolicy(service.runner.StateDirectory(), DefaultModelPolicy)
}

// admitModel checks model against the policy recorded now. A policy that
// cannot be read refuses: the gate never fails open.
func (service *AgentSessionService) admitModel(model string) error {
	content, err := os.ReadFile(service.modelPolicyPath())
	if err != nil {
		return fmt.Errorf("%w: %w", ErrModelPolicy, err)
	}
	var policy ModelPolicy
	if err := json.Unmarshal(content, &policy); err != nil {
		return fmt.Errorf("%w: %s: %w", ErrModelPolicy, service.modelPolicyPath(), err)
	}
	return policy.Admit(model)
}
