# 027 — factory judgment

Tier 2. Judged against `rubric.md`.

---

You are reviewing a daemon's wiring. Three declarations in it hand back an
interface. Say what you would change about each, and why.

```go
// buildDiscoverer constructs the membership IPeerDiscoverer for the configured
// discovery mode. Config validation guarantees the mode is one of the three
// recognized values and that its mode-specific requirements are met, so the
// default arm safely handles "static".
func buildDiscoverer(cfg config.Config) warden.IPeerDiscoverer {
	switch cfg.Discovery.Mode {
	case config.DiscoveryModeTailscale:
		return discovery.NewTailscale(discovery.TailscaleConfig{
			Socket:       cfg.Discovery.Tailscale.Socket,
			PollInterval: cfg.Discovery.Tailscale.PollInterval,
		})
	case config.DiscoveryModeFile:
		return discovery.NewFile(cfg.Discovery.File, cfg.Discovery.FilePollInterval)
	default: // "static"
		// nil (not NewStatic) is deliberate: a nil Discoverer selects the
		// election manager's static semantics — membership mirrors the config
		// peer list exactly and persisted membership is ignored, so operators
		// change a static fleet by editing config + rolling restart. Passing
		// NewStatic here would flip those nodes into dynamic semantics where
		// a persisted roster overrides config edits.
		return nil
	}
}

// Anonymous is a Config.Authenticate implementation binding every session to a
// single anonymous identity. It is the explicit opt-out from authentication,
// named rather than implied by a nil hook.
func Anonymous(request *http.Request) (IIdentity, error) { return anonymous{}, nil }
```

For reference, the field `Anonymous` exists to be assigned to:

```go
	// Authenticate derives the session identity from the upgrade request.
	// Required; use Anonymous to opt out.
	Authenticate func(request *http.Request) (IIdentity, error)
```

and the only caller of `buildDiscoverer`:

```go
	discoverer := buildDiscoverer(cfg)

	mgr, err := election.NewManager(election.Config{
		// ...
		Discoverer: discoverer,
	}, tr, st, clock)
```

Third, from the same daemon's clock, which the election and watchdog state
machines are tested against:

```go
// IClock abstracts time so the state machines can be tested deterministically
// with a simulated clock. Production code uses NewRealClock.
type IClock interface {
	Now() time.Time
	After(d time.Duration) <-chan time.Time
	NewTimer(d time.Duration) ITimer
	NewTicker(d time.Duration) ITicker
}

// ITimer mirrors time.Timer behind an interface.
type ITimer interface {
	C() <-chan time.Time
	// Stop prevents the timer from firing; it reports whether it stopped
	// a pending fire (same semantics as time.Timer.Stop).
	Stop() bool
	// Reset re-arms the timer with duration d (same semantics as
	// time.Timer.Reset: only call on stopped/drained timers).
	Reset(d time.Duration) bool
}

// RealClock is the production IClock, backed by the time package.
type RealClock struct{}

// The two methods below return interfaces because IClock declares them that
// way: a simulated clock has to be able to hand back a simulated timer, so the
// result type belongs to the interface and not to this implementation. That is
// why the house rule governs functions rather than methods.
func (RealClock) NewTimer(d time.Duration) ITimer   { return realTimer{time.NewTimer(d)} }
func (RealClock) NewTicker(d time.Duration) ITicker { return realTicker{time.NewTicker(d)} }

type realTimer struct{ t *time.Timer }

func (t realTimer) C() <-chan time.Time        { return t.t.C }
func (t realTimer) Stop() bool                 { return t.t.Stop() }
func (t realTimer) Reset(d time.Duration) bool { return t.t.Reset(d) }
```

The simulated clock in the test package implements `ITimer` with its own
`fakeTimer`, holding a channel the fake fires and an id it uses to cancel and
re-arm the waiter.
