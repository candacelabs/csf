package opencode

import (
	"fmt"
	"strings"
	"time"

	candaceosv1 "github.com/candacelabs/csf/proto/candace/candaceos/v1"
	"github.com/candacelabs/csf/services/candaceos/harness"
)

// PinnedServerVersion is the single OpenCode server version this package is
// contracted against. Start refuses to attach to any other version rather than
// projecting a transcript whose shape it cannot vouch for, so the sidecar image
// and this constant are upgraded together.
const PinnedServerVersion = "1.18.21"

// systemInstructions are the Core-owned invariants plus the workspace-scoped
// permissions this backend adds. Every prompt carries them, including the
// replacement prompt submitted by active-turn steering.
const systemInstructions = harness.ClawSystemInvariants + `Work only inside the supplied workspace. You may inspect, edit, build, and test workspace files. Never change host firewall, systemd, kernel, Docker daemon, Tailscale ACL, public routing, or files outside the workspace. Never read secrets. Do not deploy, push, merge, publish, restart services, or claim a fleet change: CandaceOS Core retains those approval and reconciliation boundaries.`

const (
	// startupHydrationAttempts bounds how often Start re-reads the transcript
	// while the session's status keeps moving under it.
	startupHydrationAttempts = 3
	// startupHydrationRetryDelay spaces those attempts.
	startupHydrationRetryDelay = 25 * time.Millisecond
	// minimumPollInterval floors the configured interval so a configuration
	// that bypassed ValidateOpenCodeConfig cannot spin the poller or panic
	// time.NewTicker.
	minimumPollInterval = time.Millisecond
	// minimumRequestTimeout floors the configured timeout for the same reason.
	minimumRequestTimeout = time.Millisecond
)

// promptModel is the parsed configuration form of the model selector. It is not
// a provider wire type: the SDK adapter converts it to the generated
// SessionPromptParamsModel at submission.
type promptModel struct {
	ProviderID string
	ModelID    string
}

// parsePromptModel splits "providerID/modelID" at its first separator, so a
// provider-qualified model such as "openrouter/openai/gpt-5.4-nano" keeps its
// remaining path in the model ID. It reports ErrModel for anything else.
func parsePromptModel(value string) (promptModel, error) {
	providerID, modelID, found := strings.Cut(value, "/")
	if !found || providerID == "" || modelID == "" || strings.HasSuffix(modelID, "/") {
		return promptModel{}, fmt.Errorf("%w: %q", ErrModel, value)
	}
	return promptModel{ProviderID: providerID, ModelID: modelID}, nil
}

func pollInterval(config *candaceosv1.OpenCodeConfig) time.Duration {
	return max(time.Duration(config.GetPollInterval()), minimumPollInterval)
}

func requestTimeout(config *candaceosv1.OpenCodeConfig) time.Duration {
	return max(time.Duration(config.GetRequestTimeout()), minimumRequestTimeout)
}

func queueCapacity(config *candaceosv1.OpenCodeConfig) int {
	return int(config.GetQueueCapacity())
}
