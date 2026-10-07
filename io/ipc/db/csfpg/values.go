// Copyright 2026 Candace Labs

package csfpg

import (
	"time"

	"github.com/jackc/pgx/v5/pgtype"
)

// Timestamp is the pgx value of a Go instant: the zero time is NULL, and a
// set time is UTC at PostgreSQL's microsecond precision, so what a service
// compares in Go is what the database stored.
func Timestamp(value time.Time) pgtype.Timestamptz {
	if value.IsZero() {
		return pgtype.Timestamptz{}
	}
	return pgtype.Timestamptz{Time: value.UTC().Truncate(time.Microsecond), Valid: true}
}

// Time is the Go instant of a pgx timestamp: NULL is the zero time, and a
// set value is read in UTC.
func Time(value pgtype.Timestamptz) time.Time {
	if !value.Valid {
		return time.Time{}
	}
	return value.Time.UTC()
}
