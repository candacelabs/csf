// Package customharness is a complete harness implementation compiled outside
// the deploy source tree.
package customharness

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"

	deployv1 "github.com/candacelabs/csf/proto/candace/deploy/v1"
	"github.com/candacelabs/csf/services/harness"
	"google.golang.org/protobuf/types/known/timestamppb"

	"example.com/candace-external-consumer/steering"
)

const implementation = "external-echo"

// Factory constructs the external echo runtime around this repository's own
// steering service.
type Factory struct {
	steering *steering.Service
}

var _ harness.IFactory = Factory{}

// NewFactory returns the custom factory installed by the external binary. The
// steering service is a component this repository composes alongside Core, so
// the harness captures a value Core only ordered.
func NewFactory(steeringService *steering.Service) Factory {
	return Factory{steering: steeringService}
}

// New binds the custom runtime to Core's host capabilities.
func (factory Factory) New(
	harnessContext *deployv1.HarnessContext,
	host harness.IHost,
) (*harness.Instance, error) {
	if err := deployv1.ValidateHarnessContext(harnessContext); err != nil {
		return nil, fmt.Errorf("external echo harness context: %w", err)
	}
	if host == nil {
		return nil, errors.New("external echo harness requires Core host capabilities")
	}
	if factory.steering == nil {
		return nil, errors.New("external echo harness requires a steering service")
	}
	identity := &deployv1.HarnessRuntimeIdentity{
		Backend:        deployv1.HarnessBackend_HARNESS_BACKEND_EMBEDDED,
		Model:          "echo-v1",
		Implementation: implementation,
	}
	if err := deployv1.ValidateHarnessRuntimeIdentity(identity); err != nil {
		return nil, fmt.Errorf("external echo identity: %w", err)
	}
	return &harness.Instance{
		Runtime:  newRuntime(host, factory.steering),
		Identity: identity,
	}, nil
}

// Runtime publishes an assistant echo and returns to idle for every prompt.
type Runtime struct {
	host     harness.IHost
	steering *steering.Service
	runner   *harness.Runner[struct{}]
	sequence atomic.Uint64
}

var _ harness.IRuntime = (*Runtime)(nil)

func newRuntime(host harness.IHost, steeringService *steering.Service) *Runtime {
	runtime := &Runtime{host: host, steering: steeringService}
	runtime.runner = harness.NewRunner[struct{}](nil, nil)
	return runtime
}

// Start creates the external harness session.
func (runtime *Runtime) Start(ctx context.Context) (*deployv1.HarnessSession, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := runtime.runner.BeginStart(); err != nil {
		return nil, fmt.Errorf("starting external echo runner: %w", err)
	}
	if err := runtime.runner.Install(runtime.send, func(ctx context.Context) error {
		return ctx.Err()
	}, nil); err != nil {
		return nil, fmt.Errorf("installing external echo adapter: %w", err)
	}
	session := &deployv1.HarnessSession{Id: "external-echo-session"}
	if err := deployv1.ValidateHarnessSession(session); err != nil {
		return nil, fmt.Errorf("external echo session: %w", err)
	}
	return session, nil
}

// Activate has no replay to publish.
func (runtime *Runtime) Activate(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	runtime.runner.Activate(nil)
	return nil
}

// Send publishes the custom reply through Core's event boundary.
func (runtime *Runtime) Send(
	ctx context.Context,
	prompt *deployv1.HarnessPrompt,
) error {
	return runtime.runner.Send(ctx, prompt)
}

func (runtime *Runtime) send(
	ctx context.Context,
	prompt *deployv1.HarnessPrompt,
) error {
	if err := deployv1.ValidateHarnessPrompt(prompt); err != nil {
		return fmt.Errorf("external echo prompt: %w", err)
	}
	runtime.steering.Observe(prompt.GetContent())
	firstEventID, secondEventID := runtime.nextEventIDs()
	now := timestamppb.Now()
	if err := runtime.publish(ctx, &deployv1.HarnessEvent{
		Id: firstEventID, RunId: prompt.GetRunId(),
		At: now,
		Payload: &deployv1.HarnessEvent_AssistantMessage{
			AssistantMessage: &deployv1.HarnessAssistantMessage{
				MessageId: firstEventID,
				Content:   "external echo: " + prompt.GetContent(),
			},
		},
	}); err != nil {
		return err
	}
	return runtime.publish(ctx, &deployv1.HarnessEvent{
		Id: secondEventID, RunId: prompt.GetRunId(),
		At:      now,
		Payload: &deployv1.HarnessEvent_Idle{Idle: &deployv1.HarnessIdle{}},
	})
}

func (runtime *Runtime) nextEventIDs() (string, string) {
	sequence := runtime.sequence.Add(2)
	return fmt.Sprintf("external-event-%d", sequence-1),
		fmt.Sprintf("external-event-%d", sequence)
}

func (runtime *Runtime) publish(ctx context.Context, event *deployv1.HarnessEvent) error {
	if err := deployv1.ValidateHarnessEvent(event); err != nil {
		return fmt.Errorf("external echo event: %w", err)
	}
	if err := runtime.host.Publish(ctx, event); err != nil {
		return fmt.Errorf("publishing external echo event: %w", err)
	}
	return nil
}

// Abort has no provider-side operation to cancel.
func (runtime *Runtime) Abort(ctx context.Context) error { return runtime.runner.Abort(ctx) }

// Close prevents later prompts from publishing events.
func (runtime *Runtime) Close() error { return runtime.runner.Close() }
