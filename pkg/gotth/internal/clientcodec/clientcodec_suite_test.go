package clientcodec_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestClientCodec(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Client Codec Suite")
}
