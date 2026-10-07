package obs

import (
	"sync"
	"sync/atomic"

	"go.opentelemetry.io/otel/attribute"
)

// LabelOptionCap bounds how many distinct label values this package will hold
// a pre-built measurement option for.
//
// Every label domain in this library is already bounded — by the protocol's
// enumerations (frame kind, rejection reason, close code, patch operation,
// transition result, panic site), by application registration (event name), or
// by SourceLabelCap. The cap is the belt to those braces: a value past it is
// still recorded, correctly and with the same attributes, it is simply built
// per call rather than cached. A cache that could grow without bound would be
// its own memory finding.
const LabelOptionCap = 256

// labelOpts caches one measurement per value of a single label.
//
// sync.Map is the right structure here and not a lock: reads dominate by
// orders of magnitude, the key space is enumerated, and after the first frame
// of each kind every lookup is a read from the map's read-only half.
type labelOpts struct {
	key   string
	held  atomic.Int64
	cache sync.Map // string -> *measurement
}

func (l *labelOpts) of(value string) *measurement {
	if m, ok := l.cache.Load(value); ok {
		return m.(*measurement)
	}
	m := newMeasurement(attribute.String(l.key, value))
	if l.held.Load() < LabelOptionCap {
		if _, loaded := l.cache.LoadOrStore(value, m); !loaded {
			l.held.Add(1)
		}
	}
	return m
}

// pairOpts is labelOpts for the one metric carrying two labels.
type pairOpts struct {
	keyA, keyB string
	held       atomic.Int64
	cache      sync.Map // string -> *measurement
}

func (p *pairOpts) of(a, b string) *measurement {
	// The separator is a NUL because it cannot appear in a Go source constant
	// used as a label value here, so two different pairs cannot collide into
	// one cache entry.
	k := a + "\x00" + b
	if m, ok := p.cache.Load(k); ok {
		return m.(*measurement)
	}
	m := newMeasurement(attribute.String(p.keyA, a), attribute.String(p.keyB, b))
	if p.held.Load() < LabelOptionCap {
		if _, loaded := p.cache.LoadOrStore(k, m); !loaded {
			p.held.Add(1)
		}
	}
	return m
}
