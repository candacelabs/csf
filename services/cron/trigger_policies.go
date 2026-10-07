// Copyright 2026 Candace Labs

package cron

import (
	grammar "github.com/candacelabs/csf/pkg/cron"
)

type triggerPolicies struct {
	catchUp grammar.CatchUpPolicy
	overlap grammar.OverlapPolicy
}
