package copilotadapter_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	api "github.com/candacelabs/csf/services/copilot-adapter/gen/api"
)

func TestCopilotAdapter(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "copilot adapter suite")
}

func resolutionBody(decision api.ResolveDecision) api.ResolveSessionRequestJSONRequestBody {
	return api.ResolveSessionRequestJSONRequestBody{Decision: decision}
}
