package copilotadapter

import (
	"context"

	adapterconfig "github.com/candacelabs/csf/services/copilot-adapter/config"
	copilotv1 "github.com/candacelabs/csf/services/copilot-adapter/proto/candace/copilot/v1"
)

// DefaultAdapterConfig preserves the service's public entry point while the
// configuration package owns defaults and their typed projections.
func DefaultAdapterConfig() *copilotv1.AdapterConfig {
	return adapterconfig.DefaultAdapterConfig()
}

// durableTransitionContext outlives a disconnected HTTP request while still
// bounding every database compensation after an external SDK call.
func (adapter *CopilotAdapter) durableTransitionContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), adapterconfig.DurableTransitionTimeout(adapter.config))
}
