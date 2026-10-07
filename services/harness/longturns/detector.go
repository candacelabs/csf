// Package longturns detects and manages long-running turns that may indicate
// stuck execution. Turns exceeding the p99 duration threshold (knee ≈ 454s) are
// escalated with checkpoint requests and preemption signals.
package longturns

import (
	"sync"
	"time"
)

// DefaultP99Threshold is the measured p99 turn duration knee from ticket #246.
const DefaultP99Threshold = 454 * time.Second

// TurnObserver tracks the lifecycle of a running turn.
type TurnObserver struct {
	SessionID  string
	TurnNumber int
	StartTime  time.Time
	EndTime    time.Time
	Duration   time.Duration
	IsLong     bool
	QueueDepth int
}

// Detector watches running turns and escalates long ones.
type Detector struct {
	mu             sync.Mutex
	threshold      time.Duration
	currentTurns   map[string]*TurnObserver // keyed by sessionID
	completedLongs []*TurnObserver
}

// NewDetector creates a long-turn detector with the default p99 threshold.
func NewDetector() *Detector {
	return NewDetectorWithThreshold(DefaultP99Threshold)
}

// NewDetectorWithThreshold creates a detector with a custom duration threshold.
func NewDetectorWithThreshold(threshold time.Duration) *Detector {
	return &Detector{
		threshold:      threshold,
		currentTurns:   make(map[string]*TurnObserver),
		completedLongs: make([]*TurnObserver, 0),
	}
}

// StartTurn records the beginning of a turn. Returns a token that must be
// passed to EndTurn.
func (d *Detector) StartTurn(sessionID string, turnNumber int) *TurnObserver {
	d.mu.Lock()
	defer d.mu.Unlock()

	obs := &TurnObserver{
		SessionID:  sessionID,
		TurnNumber: turnNumber,
		StartTime:  time.Now(),
	}
	d.currentTurns[sessionID] = obs
	return obs
}

// EndTurn records the completion of a turn and determines if it was long.
func (d *Detector) EndTurn(observer *TurnObserver) {
	if observer == nil {
		return
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	observer.EndTime = time.Now()
	observer.Duration = observer.EndTime.Sub(observer.StartTime)
	observer.IsLong = observer.Duration > d.threshold

	if observer.IsLong {
		d.completedLongs = append(d.completedLongs, observer)
	}

	delete(d.currentTurns, observer.SessionID)
}

// GetLongTurns returns all completed long turns recorded since creation.
func (d *Detector) GetLongTurns() []*TurnObserver {
	d.mu.Lock()
	defer d.mu.Unlock()

	result := make([]*TurnObserver, len(d.completedLongs))
	copy(result, d.completedLongs)
	return result
}

// LongTurnCount returns the number of long turns detected.
func (d *Detector) LongTurnCount() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.completedLongs)
}

// GetCurrentTurnDuration returns the elapsed duration of a currently running
// turn, or zero if the turn is not found.
func (d *Detector) GetCurrentTurnDuration(sessionID string) time.Duration {
	d.mu.Lock()
	defer d.mu.Unlock()

	if obs, ok := d.currentTurns[sessionID]; ok {
		return time.Since(obs.StartTime)
	}
	return 0
}

// IsCurrentTurnLong returns true if a currently running turn has exceeded the
// threshold.
func (d *Detector) IsCurrentTurnLong(sessionID string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()

	if obs, ok := d.currentTurns[sessionID]; ok {
		return time.Since(obs.StartTime) > d.threshold
	}
	return false
}
