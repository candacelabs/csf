package main

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestWidgetHost(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "examples/widget")
}
