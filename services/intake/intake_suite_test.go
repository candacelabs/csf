// Copyright 2026 Candace Labs

package intake_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

//go:generate go run go.uber.org/mock/mockgen@v0.6.0 -source=../../ipc/net/http/client.go -destination=mock_client_test.go -package=intake_test
//go:generate go run go.uber.org/mock/mockgen@v0.6.0 -source=options.go -destination=mock_directory_test.go -package=intake_test
//go:generate go run go.uber.org/mock/mockgen@v0.6.0 -source=../relay/messenger.go -destination=mock_messenger_test.go -package=intake_test
//go:generate go run go.uber.org/mock/mockgen@v0.6.0 -source=recovery.go -destination=mock_hook_deliveries_test.go -package=intake_test
//go:generate go run go.uber.org/mock/mockgen@v0.6.0 -source=webhook_routes.go -destination=mock_session_messenger_test.go -package=intake_test

// TestIntake runs both suites: the in-package unit specs of the normalizers,
// the queue and the rate-limit arithmetic, and the external integration specs
// that drive the exported API against gomock doubles of its capabilities.
func TestIntake(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "services/intake Suite")
}
