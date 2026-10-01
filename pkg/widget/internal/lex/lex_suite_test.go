package lex_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestLex(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Widget Lex Suite")
}
