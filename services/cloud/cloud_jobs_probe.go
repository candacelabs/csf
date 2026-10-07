// Copyright 2026 Candace Labs

package cloud

import (
	"context"
	"strconv"

	"github.com/candacelabs/csf/io/net/hfjobs"
)

// A probe is the smallest job: a pinned busybox that sleeps until the
// provider's timeout stops it. It runs nothing of CSF and carries no secret,
// so it proves the panel, the Stop and both caps against the real provider
// before the first burst is paid for.
const (
	// ProbeImage is busybox 1.37, pinned by its index digest (2026-10-05).
	ProbeImage = "busybox:1.37@sha256:bdf57e528e45e4433820e045b29b4597825a1c9e38353532d90a01445013f82e"
	probeSleep = "sleep"
	// probeSleepSeconds outlasts any cap: the timeout or a cancel ends it.
	probeSleepSeconds = 86400
	kindProbe         = "probe"
)

// LaunchProbeInput is one probe job.
type LaunchProbeInput struct {
	Flavor  string  `json:"flavor" jsonschema:"the provider's hardware flavor, such as cpu-basic"`
	Purpose string  `json:"purpose" jsonschema:"what the probe is for, as the Cloud panel shows it"`
	CapUSD  float64 `json:"cap_usd" jsonschema:"its spend cap in dollars; the provider's timeout is the minutes it buys"`
}

// LaunchProbe starts one probe under its cap.
func (jobs *CloudJobs) LaunchProbe(ctx context.Context, input LaunchProbeInput) (CloudJob, error) {
	var job CloudJob
	err := jobs.do(ctx, func(ctx context.Context, ledger *Ledger) error {
		var err error
		job, err = jobs.launch(ctx, ledger, probeSpec(input.Flavor), input.Purpose, input.CapUSD, nil, nil)
		return err
	})
	return job, err
}

// probeSpec is the probe on one flavor.
func probeSpec(flavor string) hfjobs.Spec {
	return hfjobs.Spec{
		DockerImage: ProbeImage, Command: []string{probeSleep, strconv.Itoa(probeSleepSeconds)}, Flavor: flavor,
		Labels: map[string]string{labelKind: kindProbe},
	}
}
