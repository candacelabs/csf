// Copyright 2026 Candace Labs

package sessiongate

// VerdictBlocked, VerdictPassed and VerdictLimit are a question's verdicts:
// refused, passed, or refused but let through because the turn reached its
// refusal limit.
const (
	VerdictBlocked = "blocked"
	VerdictPassed  = "passed"
	VerdictLimit   = "limit"
)

// QuestionKey is one series of the question count.
type QuestionKey struct {
	Gate    string
	Class   string
	Verdict string
}
