package session

// Emit injects an event into the session that spawned an effect.
type Emit func(event Event) error
