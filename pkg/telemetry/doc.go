// Package telemetry provides dependency-light distributed trace propagation
// and structured JSONL logging over the candace.telemetry.v1 protobuf
// contracts. TraceContext is the portable boundary; this package deliberately
// does not depend on an observability SDK.
package telemetry
