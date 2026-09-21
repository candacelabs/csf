package opencode

import (
	"fmt"

	"google.golang.org/protobuf/proto"

	candaceosv1 "github.com/candacelabs/csf/proto/candace/candaceos/v1"
	"github.com/candacelabs/csf/services/candaceos/harness"
)

// Factory constructs OpenCode runtimes from one immutable, validated transport
// policy. One Factory may be reused for many runtimes; every runtime it
// produces is an independent session that shares nothing but the snapshot.
// Factory is safe for concurrent use.
type Factory struct {
	config *candaceosv1.OpenCodeConfig
}

var _ harness.IFactory = (*Factory)(nil)

// NewFactory validates config and snapshots it, so a later mutation by the
// caller cannot change how sessions are opened. It reports ErrConfigRequired
// for a nil config, the Liquid Proto validation error for a config outside its
// declared bounds, and ErrModel for a model outside the providerID/modelID
// grammar.
func NewFactory(config *candaceosv1.OpenCodeConfig) (*Factory, error) {
	if config == nil {
		return nil, ErrConfigRequired
	}
	owned := proto.Clone(config).(*candaceosv1.OpenCodeConfig)
	if err := candaceosv1.ValidateOpenCodeConfig(owned); err != nil {
		return nil, fmt.Errorf("opencode: configuration: %w", err)
	}
	if _, err := parsePromptModel(owned.GetModel()); err != nil {
		return nil, err
	}
	return &Factory{config: owned}, nil
}

// New implements harness.IFactory. It validates harnessContext, then returns a
// runtime bound to that context's workspace and the supplied host. The returned
// runtime has already started its command goroutine, so the caller owns it and
// must Close it even if Start is never called or later fails. host must remain
// usable until that Close returns.
//
// The advertised identity is fixed: this backend has workspace-write and
// active-turn steering capabilities, and reports the configured model.
func (factory *Factory) New(
	harnessContext *candaceosv1.HarnessContext,
	host harness.IHost,
) (*harness.Instance, error) {
	if err := candaceosv1.ValidateHarnessContext(harnessContext); err != nil {
		return nil, fmt.Errorf("opencode: harness context: %w", err)
	}
	if host == nil {
		return nil, ErrHostRequired
	}
	owned := proto.Clone(factory.config).(*candaceosv1.OpenCodeConfig)
	workspace := harnessContext.GetWorkspace()
	sdk, err := newSDKAdapter(owned, workspace, nil)
	if err != nil {
		return nil, err
	}
	runtime, err := newRuntime(owned, workspace, host, sdk)
	if err != nil {
		return nil, err
	}
	return &harness.Instance{
		Runtime: runtime,
		Identity: &candaceosv1.HarnessRuntimeIdentity{
			Backend:        candaceosv1.HarnessBackend_HARNESS_BACKEND_OPENCODE,
			Model:          factory.config.GetModel(),
			Implementation: harness.BackendName(candaceosv1.HarnessBackend_HARNESS_BACKEND_OPENCODE),
			Capabilities: []candaceosv1.HarnessCapability{
				candaceosv1.HarnessCapability_HARNESS_CAPABILITY_WORKSPACE_WRITE,
				candaceosv1.HarnessCapability_HARNESS_CAPABILITY_ACTIVE_TURN_STEERING,
			},
		},
	}, nil
}
