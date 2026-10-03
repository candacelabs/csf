// Copyright 2026 Candace Labs

package jobs

import "errors"

// The defined errors a caller can test for with errors.Is.
var (
	ErrDatabaseRequired       = errors.New("jobs: a job database is required")
	ErrInvalidBudget          = errors.New("jobs: budget account needs a valid name and a non-negative limit")
	ErrKindsRequired          = errors.New("jobs: a ledger needs at least one valid, distinct job kind")
	ErrInvalidExecutor        = errors.New("jobs: executor needs a valid, distinct name and a value")
	ErrInvalidAdmission       = errors.New("jobs: invalid admission")
	ErrUnknownKind            = errors.New("jobs: job kind is not served by this ledger")
	ErrIdentityReused         = errors.New("jobs: job identity reused with different inputs")
	ErrBudgetExhausted        = errors.New("jobs: budget account cannot cover the reservation")
	ErrBudgetChanged          = errors.New("jobs: budget account limit is immutable after creation")
	ErrBudgetNotOpened        = errors.New("jobs: budget account has not admitted a job yet")
	ErrJobNotFound            = errors.New("jobs: job not found")
	ErrInvalidObservation     = errors.New("jobs: invalid observation")
	ErrMetricRedefined        = errors.New("jobs: metric definition changed")
	ErrUnregisteredMetric     = errors.New("jobs: measurement names an undeclared metric")
	ErrMetricLimit            = errors.New("jobs: metric registry capacity exceeded")
	ErrConflictingMeasurement = errors.New("jobs: conflicting measurement replay")
	ErrOutsideAdmission       = errors.New("jobs: measurement outside the admitted units")
	ErrIncompleteCompletion   = errors.New("jobs: completion requires every admitted unit")
	ErrNoExternalIdentity     = errors.New("jobs: executor returned no external job identity")
	ErrForeignResource        = errors.New("jobs: external resource ownership or admitted image mismatch")
)
