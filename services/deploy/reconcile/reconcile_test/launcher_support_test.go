// Copyright 2026 Candace Labs

//go:build acceptance

package reconcile_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/io/ipc/proc"
)

// hostLauncher is the real process capability a binary grants, for specs
// that drive Git and Compose as real child processes.
func hostLauncher() *proc.HostLauncher {
	GinkgoHelper()
	launcher, err := proc.NewHostLauncher()
	Expect(err).NotTo(HaveOccurred())
	return launcher
}
