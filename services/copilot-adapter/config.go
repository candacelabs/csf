package copilotadapter

import (
	"context"
	_ "embed"
	"fmt"
	"time"

	"google.golang.org/protobuf/encoding/protojson"

	copilotv1 "github.com/candacelabs/csf/services/copilot-adapter/proto/candace/copilot/v1"
)

// defaultsDocument is the DECLARED default for every tunable the adapter
// reads. The numbers live here and in the bounds compiled into
// copilotv1.ValidateAdapterConfig from proto/candace/copilot/v1/adapter.proto;
// no Go source in this package declares one (operator ruling, PR #165).
//
//go:embed config/defaults.json
var defaultsDocument []byte

// DefaultAdapterConfig parses the embedded declaration into the generated
// message. It panics only if the checked-in document stops satisfying its own
// contract, which the package's own spec catches before a build ships.
func DefaultAdapterConfig() *copilotv1.AdapterConfig {
	config, err := parseAdapterConfig(defaultsDocument)
	if err != nil {
		panic(fmt.Sprintf("copilot-adapter: the embedded default configuration is invalid: %v", err))
	}
	return config
}

// parseAdapterConfig reads one ProtoJSON configuration document and holds it
// to the contract's refinements.
func parseAdapterConfig(document []byte) (*copilotv1.AdapterConfig, error) {
	config := &copilotv1.AdapterConfig{}
	if err := protojson.Unmarshal(document, config); err != nil {
		return nil, fmt.Errorf("copilot-adapter: parse the configuration document: %w", err)
	}
	if err := copilotv1.ValidateAdapterConfig(config); err != nil {
		return nil, fmt.Errorf("copilot-adapter: %w", err)
	}
	return config, nil
}

// eventStreamPoll is the declared poll interval as a duration.
func (adapter *CopilotAdapter) eventStreamPoll() time.Duration {
	return time.Duration(adapter.config.GetEventStreamPollMillis()) * time.Millisecond
}

func (adapter *CopilotAdapter) assistantDeltaFlushInterval() time.Duration {
	return time.Duration(adapter.config.GetAssistantDeltaFlushMillis()) * time.Millisecond
}

func (adapter *CopilotAdapter) assistantDeltaMaxBytes() int {
	return int(adapter.config.GetAssistantDeltaMaxBytes())
}

func (adapter *CopilotAdapter) assistantDeltaMaxEvents() int {
	return int(adapter.config.GetAssistantDeltaMaxEvents())
}

func (adapter *CopilotAdapter) assistantDeltaSourceMaxBytes() int {
	return int(adapter.config.GetAssistantDeltaSourceMaxBytes())
}

// eventStreamPage is the declared number of rows one stream poll drains.
func (adapter *CopilotAdapter) eventStreamPage() int32 {
	return int32(adapter.config.GetEventStreamPageSize())
}

// defaultPageLimit is the declared page size a list endpoint uses when the
// caller names none.
func (adapter *CopilotAdapter) defaultPageLimit() int32 {
	return int32(adapter.config.GetDefaultPageLimit())
}

// durableTransitionContext outlives a disconnected HTTP request while still
// bounding every database compensation after an external SDK call.
func (adapter *CopilotAdapter) durableTransitionContext() (context.Context, context.CancelFunc) {
	timeout := time.Duration(adapter.config.GetDurableTransitionTimeoutMillis()) * time.Millisecond
	return context.WithTimeout(context.Background(), timeout)
}
