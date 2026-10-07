// Copyright 2026 Candace Labs

package cron

import (
	"time"

	"github.com/candacelabs/csf/io/kernel/clock"
)

type configuration struct {
	store         IStore
	clock         clock.IClock
	triggers      []declaredTrigger
	leaseDuration time.Duration
	catchUpLimit  int
	leaseOwner    string
}
