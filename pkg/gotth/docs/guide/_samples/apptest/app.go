// Package apptest is the compiled source for docs/guide/testing-your-app.md:
// a small application, and the specs that hold it to the library's contracts.
package apptest

import (
	"context"
	"net/http"

	"github.com/a-h/templ"

	"github.com/candacelabs/csf/pkg/gotth/live"
)

const (
	EventInc   = "count.inc"
	EventReset = "count.reset"

	FragmentValue = "count.value"
	FragmentLabel = "count.label"
)

// State is one session's view.
type State struct {
	N      int
	Resets int
}

// Reduce is the pure state transition under test.
func Reduce(s State, ev live.Event) (State, []live.Effect[User]) {
	switch ev.Name {
	case EventInc:
		s.N++
	case EventReset:
		s.N = 0
		s.Resets++
	}
	return s, nil
}

// Config is the application, built by a function so a spec can construct one
// without a running server.
func Config(origins []string) live.Config[State, User] {
	return live.Config[State, User]{
		Init: func(ctx context.Context, session live.Session[User]) (State, []live.Effect[User], error) {
			return State{}, nil, nil
		},
		Reduce: Reduce,
		Fragments: []live.Fragment[State]{
			{
				ID:     FragmentValue,
				Render: func(s State) templ.Component { return ValueRegion(s) },
				Dirty:  func(prev, next State) bool { return prev.N != next.N },
			},
			{
				ID:     FragmentLabel,
				Render: func(s State) templ.Component { return LabelRegion(s) },
				// Deliberately narrow, and correct: the label renders Resets
				// and nothing else. Widen it and AssertDirtyComplete still
				// passes — over-declaring is free in correctness. Narrow it to
				// nothing and AssertDirtyComplete fails, which is the point.
				Dirty: func(prev, next State) bool { return prev.Resets != next.Resets },
			},
		},
		Events:       []string{EventInc, EventReset},
		Origins:      origins,
		Authenticate: func(request *http.Request) (User, error) { return Guest, nil },
		Authorize:    Authorize,
		CSRF:         live.NoCSRFCheck,
	}
}

// User is the identity this application binds a session to.
//
// One type rather than two since 2026-09-03: a Config carries its identity type
// as a type parameter, so an application has exactly one, and "an admin" and "a
// guest" are two VALUES of it rather than two types. That is also the honest
// model — a real application authenticates one kind of principal and reads a
// role off it.
type User struct {
	// Name is the subject, and what Authorize compares.
	Name string
}

// Subject is the stable identifier the library logs and counts sessions by.
func (u User) Subject() string { return u.Name }

// Admin and Guest are the two identities the specs bind a session to.
var (
	Admin = User{Name: "admin"}
	Guest = User{Name: "guest"}
)

// Authorize is a hook a spec can call directly, given a Session.
func Authorize(_ context.Context, sess live.Session[User], ev live.Event) error {
	if ev.Name == EventReset && sess.Identity().Subject() != "admin" {
		return &live.DenyError{Reason: "only an admin may reset"}
	}
	return nil
}
