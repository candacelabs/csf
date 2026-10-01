package copilotadapter

import api "github.com/candacelabs/csf/services/copilot-adapter/gen/api"

// OpenAPI owns the append-only numeric identities. This dictionary supplies
// default display text; neither recovery nor classification matches prose.
var sessionFailureDescriptions = map[api.FailureCode]string{
	api.FailureCodeProviderShutdown:       "the Copilot session failed",
	api.FailureCodeSessionCreationFailed:  "session creation failed",
	api.FailureCodeProviderSessionMissing: "the Copilot SDK session no longer exists and could not be restored",
	api.FailureCodeWorktreeUnavailable:    "the worktree is unavailable or no longer matches its registered identity",
}

func sessionFailureDescription(code api.FailureCode) string {
	if description, ok := sessionFailureDescriptions[code]; ok {
		return description
	}
	return "session failed"
}
