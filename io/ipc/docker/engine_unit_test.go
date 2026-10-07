// Copyright 2026 Candace Labs

package docker

import (
	"context"
	"errors"
	"os"
	"strings"

	"github.com/moby/moby/client"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

const integrationEnv = "CSF_DOCKER_SANDBOX_IMAGE"

// The real Engine, when the environment names an image already present
// locally: CSF_DOCKER_SANDBOX_IMAGE=alpine:3 with the Engine socket mounted.
var _ = Describe("ContainerHost against the real Engine", func() {
	It("runs a sandboxed command with no network and removes it", func(ctx SpecContext) {
		image := os.Getenv(integrationEnv)
		if image == "" {
			Skip(integrationEnv + " is unset: the real-Engine spec did not run")
		}
		host, err := NewContainerHost()
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(host.Close)

		result, err := host.RunSandboxed(ctx, SandboxSpec{
			Image:   image,
			Command: []string{"sh", "-c", "echo sandboxed; ls /sys/class/net; echo diagnostics >&2; touch /forbidden"},
		})

		var exitError *ExitError
		Expect(errors.As(err, &exitError)).To(BeTrue(), "the read-only root refuses the write: %v", err)
		Expect(string(result.Stdout)).To(HavePrefix("sandboxed\n"))
		Expect(strings.Fields(strings.TrimPrefix(string(result.Stdout), "sandboxed\n"))).To(Equal([]string{"lo"}),
			"network none leaves only loopback")
		Expect(string(result.Stderr)).To(ContainSubstring("diagnostics"))
		_, err = host.ContainerInspect(context.WithoutCancel(ctx), result.ContainerID, client.ContainerInspectOptions{})
		Expect(err).To(HaveOccurred(), "the sandbox was removed")
	})
})
