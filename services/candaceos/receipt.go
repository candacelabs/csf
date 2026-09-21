package candaceos

import (
	"fmt"
	"regexp"
	"time"
)

var receiptKindPattern = regexp.MustCompile(`^[a-z][a-z0-9]*(?:[._-][a-z0-9]+)*$`)

// ReceiptKind is an extensible, machine-readable event name. The common run
// lifecycle names below are conveniences; daemons may add names in the same
// lowercase dotted form without changing this package.
type ReceiptKind string

const (
	ReceiptRunQueued         ReceiptKind = "run.queued"
	ReceiptApprovalRequested ReceiptKind = "approval.requested"
	ReceiptApprovalApproved  ReceiptKind = "approval.approved"
	ReceiptApprovalDenied    ReceiptKind = "approval.denied"
	ReceiptRunStarted        ReceiptKind = "run.started"
	ReceiptRunSucceeded      ReceiptKind = "run.succeeded"
	ReceiptRunFailed         ReceiptKind = "run.failed"
	ReceiptRunCanceled       ReceiptKind = "run.canceled"
)

// Receipt is one immutable observation in a run's append-only history.
type Receipt struct {
	ID       string      `json:"id" yaml:"id"`
	RunID    string      `json:"run_id" yaml:"run_id"`
	Sequence uint64      `json:"sequence" yaml:"sequence"`
	Kind     ReceiptKind `json:"kind" yaml:"kind"`
	At       time.Time   `json:"at" yaml:"at"`
	Summary  string      `json:"summary" yaml:"summary"`
}

// Validate checks the shape of one receipt independently of a log.
func (receipt Receipt) Validate() error {
	if err := validateID("id", receipt.ID); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidReceipt, err)
	}
	if err := validateID("run_id", receipt.RunID); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidReceipt, err)
	}
	if receipt.Sequence == 0 {
		return fmt.Errorf("%w: sequence must start at 1", ErrInvalidReceipt)
	}
	if !receiptKindPattern.MatchString(string(receipt.Kind)) {
		return fmt.Errorf("%w: kind must be a lowercase dotted event name", ErrInvalidReceipt)
	}
	if receipt.At.IsZero() {
		return fmt.Errorf("%w: at is required", ErrInvalidReceipt)
	}
	if receipt.Summary == "" {
		return fmt.Errorf("%w: summary is required", ErrInvalidReceipt)
	}
	return nil
}

// ReceiptLog owns one run's ordered receipt history. Its entries are private,
// Entries returns a copy, and Append is the only operation that changes it.
type ReceiptLog struct {
	runID   string
	entries []Receipt
}

// NewReceiptLog starts an empty append-only log for one run.
func NewReceiptLog(runID string) (*ReceiptLog, error) {
	if err := validateID("run_id", runID); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrReceiptAppend, err)
	}
	return &ReceiptLog{runID: runID}, nil
}

// RestoreReceiptLog validates ordered durable history before accepting it.
func RestoreReceiptLog(runID string, receipts []Receipt) (*ReceiptLog, error) {
	log, err := NewReceiptLog(runID)
	if err != nil {
		return nil, err
	}
	for _, receipt := range receipts {
		if err := log.Append(receipt); err != nil {
			return nil, err
		}
	}
	return log, nil
}

// Append extends history with exactly the next sequence number. It rejects
// replacement, insertion, cross-run events, duplicate IDs, and time reversal.
func (log *ReceiptLog) Append(receipt Receipt) error {
	if log == nil {
		return fmt.Errorf("%w: log is nil", ErrReceiptAppend)
	}
	if err := receipt.Validate(); err != nil {
		return fmt.Errorf("%w: %w", ErrReceiptAppend, err)
	}
	if receipt.RunID != log.runID {
		return fmt.Errorf("%w: receipt run_id %q does not match log run_id %q", ErrReceiptAppend, receipt.RunID, log.runID)
	}
	expected := uint64(len(log.entries)) + 1
	if receipt.Sequence != expected {
		return fmt.Errorf("%w: sequence must be %d, got %d", ErrReceiptAppend, expected, receipt.Sequence)
	}
	for _, existing := range log.entries {
		if existing.ID == receipt.ID {
			return fmt.Errorf("%w: duplicate receipt id %q", ErrReceiptAppend, receipt.ID)
		}
	}
	if len(log.entries) > 0 && receipt.At.Before(log.entries[len(log.entries)-1].At) {
		return fmt.Errorf("%w: receipt time cannot precede the previous receipt", ErrReceiptAppend)
	}
	log.entries = append(log.entries, receipt)
	return nil
}

// Entries returns an independent snapshot of the receipt history.
func (log *ReceiptLog) Entries() []Receipt {
	if log == nil {
		return nil
	}
	entries := make([]Receipt, len(log.entries))
	copy(entries, log.entries)
	return entries
}

// Len reports the number of appended receipts.
func (log *ReceiptLog) Len() int {
	if log == nil {
		return 0
	}
	return len(log.entries)
}
