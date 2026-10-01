package config

import (
	_ "embed"
	"fmt"
	"time"

	copilotv1 "github.com/candacelabs/csf/services/copilot-adapter/proto/candace/copilot/v1"
	"google.golang.org/protobuf/encoding/protojson"
)

// defaultConfigurationDocument is the declared default for every tunable the
// adapter reads. Bounds remain owned by the generated Liquid Proto validator.
//
//go:embed defaults.json
var defaultConfigurationDocument []byte

// DefaultAdapterConfig parses the checked-in default document into the
// generated contract. The document is checked by the service's configuration tests.
func DefaultAdapterConfig() *copilotv1.AdapterConfig {
	configuration, err := ParseAdapterConfig(defaultConfigurationDocument)
	if err != nil {
		panic(fmt.Sprintf("copilot-adapter: the embedded default configuration is invalid: %v", err))
	}
	return configuration
}

// ParseAdapterConfig parses one ProtoJSON configuration document and validates
// its fields against the generated Liquid Proto contract.
func ParseAdapterConfig(document []byte) (*copilotv1.AdapterConfig, error) {
	configuration := &copilotv1.AdapterConfig{}
	if err := protojson.Unmarshal(document, configuration); err != nil {
		return nil, fmt.Errorf("copilot-adapter: parse the configuration document: %w", err)
	}
	if err := copilotv1.ValidateAdapterConfig(configuration); err != nil {
		return nil, fmt.Errorf("copilot-adapter: %w", err)
	}
	return configuration, nil
}

// EventStreamPollInterval converts the declared polling interval to a duration.
func EventStreamPollInterval(configuration *copilotv1.AdapterConfig) time.Duration {
	return time.Duration(configuration.GetEventStreamPollMillis()) * time.Millisecond
}

// EventStreamPageSize returns the declared number of rows drained by one poll.
func EventStreamPageSize(configuration *copilotv1.AdapterConfig) int32 {
	return int32(configuration.GetEventStreamPageSize())
}

// DefaultPageLimit returns the declared fallback for list endpoint page sizes.
func DefaultPageLimit(configuration *copilotv1.AdapterConfig) int32 {
	return int32(configuration.GetDefaultPageLimit())
}

// DurableTransitionTimeout converts the configured persistence budget.
func DurableTransitionTimeout(configuration *copilotv1.AdapterConfig) time.Duration {
	return time.Duration(configuration.GetDurableTransitionTimeoutMillis()) * time.Millisecond
}

// AssistantDeltaMaxBytes returns the durable event's byte limit.
func AssistantDeltaMaxBytes(configuration *copilotv1.AdapterConfig) int {
	return int(configuration.GetAssistantDeltaMaxBytes())
}

// AssistantDeltaFlushInterval converts the in-memory batch window to a duration.
func AssistantDeltaFlushInterval(configuration *copilotv1.AdapterConfig) time.Duration {
	return time.Duration(configuration.GetAssistantDeltaFlushMillis()) * time.Millisecond
}

// AssistantDeltaMaxEvents returns the maximum source receipts retained per batch.
func AssistantDeltaMaxEvents(configuration *copilotv1.AdapterConfig) int {
	return int(configuration.GetAssistantDeltaMaxEvents())
}

// AssistantDeltaSourceMaxBytes returns the accepted source delta byte limit.
func AssistantDeltaSourceMaxBytes(configuration *copilotv1.AdapterConfig) int {
	return int(configuration.GetAssistantDeltaSourceMaxBytes())
}
