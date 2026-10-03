package cron

import (
	"crypto/sha256"
	"encoding/hex"
	"time"
)

// OccurrenceStatus is the durable state of one occurrence: running under a
// lease, or one of the four terminal states.
type OccurrenceStatus string

const (
	OccurrenceRunning   OccurrenceStatus = "running"
	OccurrenceSucceeded OccurrenceStatus = "succeeded"
	OccurrenceFailed    OccurrenceStatus = "failed"
	OccurrenceCanceled  OccurrenceStatus = "canceled"
	OccurrenceSkipped   OccurrenceStatus = "skipped"
)

// Completion reports whether the status is one an operation's completion
// records: succeeded, failed or canceled. A skipped occurrence never ran.
func (status OccurrenceStatus) Completion() bool {
	switch status {
	case OccurrenceSucceeded, OccurrenceFailed, OccurrenceCanceled:
		return true
	default:
		return false
	}
}

// OccurrenceRecord is the durable execution record of one trigger's
// scheduled instant. LeaseToken is the fencing secret the store checks and
// is deliberately omitted from JSON snapshots.
type OccurrenceRecord struct {
	ID           string           `json:"id"`
	TriggerName  string           `json:"trigger_name"`
	ScheduledAt  time.Time        `json:"scheduled_at"`
	Status       OccurrenceStatus `json:"status"`
	Attempt      uint32           `json:"attempt"`
	StartedAt    time.Time        `json:"started_at,omitempty"`
	FinishedAt   time.Time        `json:"finished_at,omitempty"`
	LeaseOwner   string           `json:"lease_owner,omitempty"`
	LeaseToken   string           `json:"-"`
	LeaseUntil   time.Time        `json:"lease_until,omitempty"`
	Error        string           `json:"error,omitempty"`
	SkipReason   string           `json:"skip_reason,omitempty"`
	LastModified time.Time        `json:"last_modified"`
}

const (
	// occurrenceIDPrefix marks an occurrence identifier, as the store's check
	// constraint expects.
	occurrenceIDPrefix = "occ_"
	// occurrenceIDSeparator keeps a trigger name and an instant from running
	// into each other under the digest.
	occurrenceIDSeparator = "\x00"
)

// OccurrenceID is the stable identity of one trigger's scheduled instant,
// and so the idempotency key of the operation that runs it.
func OccurrenceID(triggerName string, scheduledAt time.Time) string {
	digest := sha256.Sum256([]byte(triggerName + occurrenceIDSeparator + scheduledAt.UTC().Format(time.RFC3339Nano)))
	return occurrenceIDPrefix + hex.EncodeToString(digest[:])
}

// StoreSnapshot is a point-in-time copy of the active triggers and the most
// recent occurrences a store holds.
type StoreSnapshot struct {
	Triggers    []TriggerState     `json:"triggers"`
	Occurrences []OccurrenceRecord `json:"occurrences"`
}
