package mounting

import (
	"context"
	"io"
	"net/http"
	"strconv"
	"sync"

	"github.com/a-h/templ"

	"github.com/candacelabs/csf/pkg/gotth/live"
)

// State is the quickstart's state: one number.
type State struct{ N int }

// Store is whatever the initial state actually comes from — a database, a
// cache, the row a session is a view of. It is here so that Load has something
// to read that can change between two requests, which is the whole of what this
// sample demonstrates.
type Store struct {
	mu sync.Mutex
	n  int
}

// NewStore returns a store holding n.
func NewStore(n int) *Store { return &Store{n: n} }

// Set replaces the stored value. In an application this is an effect, not a
// setter; here it is the shortest way for a spec to move the world between two
// requests.
func (st *Store) Set(n int) {
	st.mu.Lock()
	defer st.mu.Unlock()
	st.n = n
}

// Load is the one function both Init and the page handler call. That is the
// point: two places produce the first paint — the HTTP response and the
// snapshot the session sends after it connects — and they agree only because
// they are the same function of the same source.
func (st *Store) Load(ctx context.Context) (State, error) {
	if err := ctx.Err(); err != nil {
		return State{}, err
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	return State{N: st.n}, nil
}

// Init is Config.Init. It returns exactly what Load returns.
func (st *Store) Init(ctx context.Context, session live.Session[live.AnonymousIdentity]) (State, []live.Effect[live.AnonymousIdentity], error) {
	s, err := st.Load(ctx)
	return s, nil, err
}

// App is the live application this store's state belongs to.
//
// Init is the only field this sample is about. The other seven are what
// live.New requires of any application — a reducer, a region, the event names
// it accepts, and the four security hooks, opted out of here because a sample
// with one number in it has no identities — and they are written out so this
// compiles rather than to be copied.
func (st *Store) App() *live.App[State, live.AnonymousIdentity] {
	return live.MustNew(live.Config[State, live.AnonymousIdentity]{
		Init:         st.Init,
		Reduce:       func(s State, _ live.Event) (State, []live.Effect[live.AnonymousIdentity]) { return s, nil },
		Fragments:    []live.Fragment[State]{{ID: "reading", Render: Page}},
		Events:       []string{"reading.refresh"},
		Origins:      []string{"http://127.0.0.1:8080"},
		Authenticate: live.Anonymous,
		Authorize:    live.AllowAll[live.AnonymousIdentity],
		CSRF:         live.NoCSRFCheck,
	})
}

// FirstPaint is the handler that serves the page, and it is the library's own:
// (*live.App).PageHandler calls Config.Init — st.Init, above — on every request
// and renders Page from the state it returns. So the bytes a visitor is served
// and the first snapshot their session receives are not merely written from one
// function, they ARE one function.
//
// This method used to be that handler, written by hand: load, check the error,
// hand the state to templ.Handler. The library provides it now, and a sample
// that kept the hand-rolled version would be teaching a reader to write code
// they have been given — so what is left here is the wiring, which is the part
// that is theirs.
//
// The alternative it exists to make unwritable is templ.Handler(Page(State{}))
// registered once: it builds the component at start-up and serves those bytes
// forever, which is correct only while Init also returns the zero value and
// fails silently the moment Init reads anything. PageHandler cannot be given a
// state value, only the function that renders one. The last spec in
// mounting_test.go pins that failure so it stays recognisable.
func (st *Store) FirstPaint() http.Handler {
	return st.App().PageHandler(Page)
}

// Page stands in for the templ component docs/quickstart.md §3 writes. It is
// written as Go here so this sample needs no generated file; in an application
// it is `templ Page(s State)` and the signature is the one below.
func Page(s State) templ.Component {
	return templ.ComponentFunc(func(_ context.Context, w io.Writer) error {
		_, err := io.WriteString(w, "<output>"+strconv.Itoa(s.N)+"</output>")
		return err
	})
}
