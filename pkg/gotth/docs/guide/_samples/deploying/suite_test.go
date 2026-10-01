package deploying_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestDeploying(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Deploying Suite")
}
