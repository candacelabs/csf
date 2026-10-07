// Copyright 2026 Candace Labs

package harness

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	"github.com/candacelabs/csf/csf"
	"github.com/candacelabs/csf/pkg/atomicfile"
	pb "github.com/candacelabs/csf/proto/candace/brainspine/v1"
	harnessv1 "github.com/candacelabs/csf/proto/candace/harness/v1"
	"github.com/candacelabs/csf/services/harness/session"
)

// ExecutorDefaultFile records the operator's last switch of the default
// executor and model, under the state directory, so a restarted host keeps it.
const ExecutorDefaultFile = "executor-default.json"

var (
	// ErrNoDefaultModel reports a default that moves recipes to an executor
	// other than the one they are written for without naming the model.
	ErrNoDefaultModel = fmt.Errorf("%w: a default executor other than %s needs a model, spelled as it spells it", csf.ErrInvalidRequest, session.ExecutorClaudeCode)
)

// WithExecutorDefault is the executor and model a recipe naming no executor
// runs on until the operator switches it; a switch recorded under the state
// directory takes precedence. The default default is Claude Code with each
// recipe's own model.
func WithExecutorDefault(executor session.Executor, model string) AgentSessionServiceOption {
	return func(service *AgentSessionService) error {
		chosen := &harnessv1.AgentExecutorDefault{Executor: string(executor), Model: model}
		if err := validExecutorDefault(chosen); err != nil {
			return fmt.Errorf("%w: %w", ErrInvalidServiceOption, err)
		}
		service.executorDefault = chosen
		return nil
	}
}

func validExecutorDefault(chosen *harnessv1.AgentExecutorDefault) error {
	if err := harnessv1.ValidateAgentExecutorDefault(chosen); err != nil {
		return fmt.Errorf("%w: %w", csf.ErrInvalidRequest, err)
	}
	if chosen.GetExecutor() != string(session.ExecutorClaudeCode) && chosen.GetModel() == "" {
		return ErrNoDefaultModel
	}
	return nil
}

func (service *AgentSessionService) executorDefaultPath() string {
	return filepath.Join(service.runner.StateDirectory(), ExecutorDefaultFile)
}

// recordedExecutorDefault is the last switch recorded under the state
// directory, or the default the host was started with.
func (service *AgentSessionService) recordedExecutorDefault() *harnessv1.AgentExecutorDefault {
	content, err := os.ReadFile(service.executorDefaultPath())
	if err != nil {
		return proto.Clone(service.executorDefault).(*harnessv1.AgentExecutorDefault)
	}
	recorded := &harnessv1.AgentExecutorDefault{}
	if err := protojson.Unmarshal(content, recorded); err != nil || validExecutorDefault(recorded) != nil {
		service.logger.Warn("harness: recorded executor default ignored", "path", service.executorDefaultPath(), "error", err)
		return proto.Clone(service.executorDefault).(*harnessv1.AgentExecutorDefault)
	}
	return recorded
}

// withExecutorDefault is recipe, which names no executor, on the default: the
// default executor and, when the default names one, its model.
func (service *AgentSessionService) withExecutorDefault(ctx context.Context, recipe *pb.AgentAssignmentRecipe) (*pb.AgentAssignmentRecipe, error) {
	current, err := service.GetExecutorDefault(ctx, &harnessv1.GetAgentExecutorDefaultRequest{})
	if err != nil {
		return nil, err
	}
	chosen := current.GetExecutorDefault()
	if chosen.GetExecutor() == string(session.ExecutorClaudeCode) && chosen.GetModel() == "" {
		return recipe, nil
	}
	resolved := proto.Clone(recipe).(*pb.AgentAssignmentRecipe)
	if chosen.GetExecutor() != string(session.ExecutorClaudeCode) {
		resolved.Executor = chosen.GetExecutor()
	}
	if chosen.GetModel() != "" {
		resolved.Model = chosen.GetModel()
	}
	return resolved, nil
}

// GetExecutorDefault reports the executor and model a recipe naming no
// executor runs on now.
func (service *AgentSessionService) GetExecutorDefault(ctx context.Context, _ *harnessv1.GetAgentExecutorDefaultRequest) (*harnessv1.GetAgentExecutorDefaultResponse, error) {
	response := &harnessv1.GetAgentExecutorDefaultResponse{}
	if err := service.command(ctx, func(table *registry) error {
		response.ExecutorDefault = proto.Clone(table.executorDefault).(*harnessv1.AgentExecutorDefault)
		return nil
	}); err != nil {
		return nil, err
	}
	return response, nil
}

// SetExecutorDefault switches the default for every session submitted from
// now on and records it under the state directory. Running sessions keep the
// executor they opened on.
func (service *AgentSessionService) SetExecutorDefault(ctx context.Context, request *harnessv1.SetAgentExecutorDefaultRequest) (*harnessv1.SetAgentExecutorDefaultResponse, error) {
	chosen := request.GetExecutorDefault()
	if err := validExecutorDefault(chosen); err != nil {
		return nil, err
	}
	// A default that names a model switches every new session to it, so it
	// is held to the model policy; one that keeps each recipe's model leaves
	// the check to each submit.
	if chosen.GetModel() != "" {
		if err := service.admitModel(chosen.GetModel()); err != nil {
			return nil, err
		}
	}
	content, err := protojson.Marshal(chosen)
	if err != nil {
		return nil, err
	}
	response := &harnessv1.SetAgentExecutorDefaultResponse{ExecutorDefault: proto.Clone(chosen).(*harnessv1.AgentExecutorDefault)}
	if err := service.command(ctx, func(table *registry) error {
		path := service.executorDefaultPath()
		if err := atomicfile.WriteFile(path, content, stateFileMode); err != nil {
			return fmt.Errorf("harness: record the executor default: %w", err)
		}
		response.Previous, table.executorDefault = table.executorDefault, proto.Clone(chosen).(*harnessv1.AgentExecutorDefault)
		return nil
	}); err != nil {
		return nil, err
	}
	service.logger.Info("harness: executor default switched", "executor", chosen.GetExecutor(), "model", chosen.GetModel(),
		"previous_executor", response.GetPrevious().GetExecutor(), "previous_model", response.GetPrevious().GetModel())
	return response, nil
}
