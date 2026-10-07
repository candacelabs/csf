// Copyright 2026 Candace Labs

package cron

import (
	grammar "github.com/candacelabs/csf/pkg/cron"
)

type declaredTrigger struct {
	name      string
	schedule  grammar.Schedule
	operation Operation
	policies  triggerPolicies
}
