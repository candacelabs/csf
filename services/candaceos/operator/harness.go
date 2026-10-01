package operator

import (
	"context"
	"fmt"

	candaceosv1 "github.com/candacelabs/csf/proto/candace/candaceos/v1"
	harnesssdk "github.com/candacelabs/csf/services/candaceos/harness"
	opencodeharness "github.com/candacelabs/csf/services/candaceos/harness/opencode"
)

// iHarnessImplementation is the narrow turn lifecycle owned by an agent runtime.
// Controller retains policy, approvals, durable run state, and UI projection.
type iHarnessImplementation interface {
	Start(ctx context.Context) (harnessStart, error)
	Send(ctx context.Context, prompt *candaceosv1.HarnessPrompt) error
	Abort(ctx context.Context) error
	Close() error
}

// harnessBinding is the selected implementation and its immutable
// operator-facing identity.
type harnessBinding struct {
	runtime  iHarnessImplementation
	identity *candaceosv1.HarnessRuntimeIdentity
}

type harnessStart struct {
	SessionID string
	// Activate publishes startup events only after Controller has installed the
	// session identity and idle state. It is nil when a harness has no replay.
	Activate func() error
}

func configureHarness(
	cfg *candaceosv1.CoreConfig,
	controller *Controller,
	factory harnesssdk.IFactory,
) (harnessBinding, error) {
	if factory != nil {
		implementation, identity, err := newHarnessAdapter(
			cfg,
			controller,
			factory,
			candaceosv1.HarnessBackend_HARNESS_BACKEND_EMBEDDED,
		)
		if err != nil {
			return harnessBinding{}, err
		}
		return harnessBinding{runtime: implementation, identity: identity}, nil
	}
	if cfg.GetHarnessBackend() == candaceosv1.HarnessBackend_HARNESS_BACKEND_OPENCODE {
		factory, err := opencodeharness.NewFactory(cfg.GetOpencode())
		if err != nil {
			return harnessBinding{}, err
		}
		implementation, identity, err := newHarnessAdapter(
			cfg,
			controller,
			factory,
			candaceosv1.HarnessBackend_HARNESS_BACKEND_OPENCODE,
		)
		if err != nil {
			return harnessBinding{}, err
		}
		return harnessBinding{runtime: implementation, identity: identity}, nil
	}
	identity, err := harnessRuntimeIdentity(cfg)
	if err != nil {
		return harnessBinding{}, err
	}
	binding := harnessBinding{identity: identity}
	switch cfg.GetHarnessBackend() {
	case candaceosv1.HarnessBackend_HARNESS_BACKEND_DEMO:
		binding.runtime = &demoHarness{controller: controller}
	case candaceosv1.HarnessBackend_HARNESS_BACKEND_COPILOT_CLI:
		binding.runtime = newCopilotHarness(cfg, controller)
	case candaceosv1.HarnessBackend_HARNESS_BACKEND_OLLAMA:
		if cfg.GetOllama() == nil {
			return harnessBinding{}, fmt.Errorf("Ollama harness configuration is required")
		}
		binding.runtime = newOllamaHarness(cfg.GetOllama(), controller)
	default:
		return harnessBinding{}, fmt.Errorf("unsupported agent harness backend %q", cfg.GetHarnessBackend())
	}
	return binding, nil
}

const clawSystemInvariants = harnesssdk.ClawSystemInvariants

const copilotSystemInstructions = clawSystemInvariants + `Work only inside the supplied workspace. Prefer existing repository primitives and ordinary Docker Compose apps. Put each deployable app in one workspace-relative directory with a standard compose.yaml and a stable Compose service name. Never change host firewall, systemd, kernel, Docker daemon, Tailscale ACL, or public routing. Never read secrets. Build and test freely in the isolated workspace, but treat a deployment, push, merge, public write, service restart, or destructive action as a proposed action that needs explicit approval. Before selecting placement, call candace_fleet_status. Use candace_reconcile_app for deployments and stops.`

const ollamaSystemInstructions = clawSystemInvariants + `Your available actions are exactly the native tools supplied with this request. Call candace_fleet_status before selecting placement. You cannot inspect, edit, build, or test workspace files. Use candace_reconcile_app only for an app that the operator has already materialized in the configured workspace; that tool always pauses for explicit approval. If the requested app is not materialized, explain what is needed instead of claiming to create or deploy it.`
