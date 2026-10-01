package main

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestDevWatcher(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Dev Watcher Suite")
}
