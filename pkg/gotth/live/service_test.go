// Copyright 2026 Candace Labs

package live_test

//go:generate go run go.uber.org/mock/mockgen@v0.6.0 -destination=mock_identity_test.go -package=live_test github.com/candacelabs/csf/pkg/gotth/live IIdentity
//go:generate go run go.uber.org/mock/mockgen@v0.6.0 -destination=mock_listener_test.go -package=live_test github.com/candacelabs/csf/io/net IListener

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	stdnet "net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"time"

	"github.com/a-h/templ"
	"github.com/coder/websocket"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/goleak"
	"go.uber.org/mock/gomock"
	"google.golang.org/grpc/test/bufconn"

	iohttp "github.com/candacelabs/csf/io/net/http"
	"github.com/candacelabs/csf/pkg/gotth/live"
	"github.com/candacelabs/csf/runtime"
)

// The integration specs for the gotth-live service as a host binary uses it:
// only the exported API, a gomock identity and a gomock socket capability, and
// an in-memory transport in place of the kernel. They cover the defined
// lifecycle — mount, serve, stop with sessions open — and every misuse the
// service refuses with a defined error.

const (
	serviceOrigin  = "https://service.example"
	serviceAddress = "in-memory:1"
	serviceSubject = "service-spec"
	serviceEvent   = "count.add"
	serviceRegion  = "count"
	serviceWatch   = "count.watch"
	serviceURL     = "ws://in-memory/live"
	// subprotocol is the wire token a browser offers on the upgrade, spelled
	// as the wire carries it: this spec is a client of that contract.
	subprotocol  = "gotth-live.v1"
	memoryBuffer = 1 << 20
	// stopBudget is generous: a stop with sessions open is a close handshake
	// per session, run concurrently, on a machine that may be loaded.
	stopBudget     = 10 * time.Second
	joinedMessage  = "gotth-live: joined every session goroutine"
	drainedMessage = "gotth-live: drained every live session"
)

type countState struct{ N int }

// newCountService builds an App whose every session runs one server-push
// effect until its context is cancelled — the shape of a feed subscription —
// so a stop has an effect goroutine to join.
func newCountService(identity *MockIIdentity, logger *slog.Logger) *live.App[countState, *MockIIdentity] {
	return live.MustNew(live.Config[countState, *MockIIdentity]{
		Init: func(_ context.Context, _ live.Session[*MockIIdentity]) (countState, []live.Effect[*MockIIdentity], error) {
			return countState{}, []live.Effect[*MockIIdentity]{{
				Source: serviceWatch,
				Run: func(ctx context.Context, _ live.Session[*MockIIdentity], _ live.Emitter) error {
					<-ctx.Done()
					return nil
				},
			}}, nil
		},
		Reduce: func(state countState, _ live.Event) (countState, []live.Effect[*MockIIdentity]) {
			state.N++
			return state, nil
		},
		Fragments: []live.Fragment[countState]{{
			ID: serviceRegion,
			Render: func(state countState) templ.Component {
				return templ.ComponentFunc(func(_ context.Context, writer io.Writer) error {
					_, err := io.WriteString(writer, strconv.Itoa(state.N))
					return err
				})
			},
		}},
		Events:       []string{serviceEvent},
		Origins:      []string{serviceOrigin},
		Authenticate: func(_ *http.Request) (*MockIIdentity, error) { return identity, nil },
		Authorize:    live.AllowAll[*MockIIdentity],
		CSRF:         live.NoCSRFCheck,
		Logger:       logger,
	})
}

// records keeps every log record so a spec can read the counts the service
// logs when it stops.
type records struct {
	mu   sync.Mutex
	kept []slog.Record
}

func (r *records) Enabled(_ context.Context, _ slog.Level) bool { return true }
func (r *records) WithAttrs(_ []slog.Attr) slog.Handler         { return r }
func (r *records) WithGroup(_ string) slog.Handler              { return r }
func (r *records) Handle(_ context.Context, record slog.Record) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.kept = append(r.kept, record.Clone())
	return nil
}

// count is the integer attribute key of the record logged with message, or -1.
func (r *records) count(message string, key string) int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	found := int64(-1)
	for _, record := range r.kept {
		if record.Message != message {
			continue
		}
		record.Attrs(func(attribute slog.Attr) bool {
			if attribute.Key == key {
				found = attribute.Value.Int64()
			}
			return true
		})
	}
	return found
}

// memoryClient dials the in-memory listener as a browser would dial the
// host's address.
func memoryClient(listener *bufconn.Listener) *http.Client {
	return &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _ string, _ string) (stdnet.Conn, error) {
			return listener.DialContext(ctx)
		},
	}}
}

var _ = Describe("The gotth-live service mounted in a host runtime", func() {
	var (
		identity *MockIIdentity
		network  *MockIListener
		logs     *records
		baseline goleak.Option
	)

	BeforeEach(func() {
		baseline = goleak.IgnoreCurrent()
		controller := gomock.NewController(GinkgoT())
		identity = NewMockIIdentity(controller)
		identity.EXPECT().Subject().Return(serviceSubject).AnyTimes()
		network = NewMockIListener(controller)
		logs = &records{}
	})

	It("joins every session goroutine when the host stops with sessions open", func(ctx SpecContext) {
		memory := bufconn.Listen(memoryBuffer)
		network.EXPECT().Listen(gomock.Any(), "tcp", serviceAddress).Return(memory, nil)

		app := newCountService(identity, slog.New(logs))
		listener, err := iohttp.NewHTTPListener(network, serviceAddress, app.Handler())
		Expect(err).NotTo(HaveOccurred())
		host, err := runtime.NewHostRuntime()
		Expect(err).NotTo(HaveOccurred())
		Expect(host.Mount("live", app)).To(Succeed())
		Expect(host.Mount("http", listener)).To(Succeed())

		runContext, stop := context.WithCancel(ctx)
		DeferCleanup(stop)
		runner := runtime.NewScope(context.Background(), "service spec host")
		stopped := make(chan error, 1)
		Expect(runner.Go(func(_ context.Context) error {
			stopped <- host.Run(runContext)
			return nil
		})).To(Succeed())
		Eventually(host.Ready).Should(BeTrue())

		client := memoryClient(memory)
		const sessions = 3
		connections := make([]*websocket.Conn, 0, sessions)
		for range sessions {
			connection, _, err := websocket.Dial(ctx, serviceURL, &websocket.DialOptions{
				HTTPClient:   client,
				HTTPHeader:   http.Header{"Origin": []string{serviceOrigin}},
				Subprotocols: []string{subprotocol},
			})
			Expect(err).NotTo(HaveOccurred())
			kind, snapshot, err := connection.Read(ctx)
			Expect(err).NotTo(HaveOccurred())
			Expect(kind).To(Equal(websocket.MessageBinary))
			Expect(snapshot).NotTo(BeEmpty(), "the session's first frame is its snapshot")
			connections = append(connections, connection)
		}
		Expect(app.ActiveConnections()).To(Equal(sessions))

		// Each client keeps reading, as a browser does, so it answers the
		// server's close handshake and reports the code it was closed with.
		readers := runtime.NewScope(context.Background(), "service spec clients")
		closes := make(chan websocket.StatusCode, sessions)
		for _, connection := range connections {
			Expect(readers.Go(func(_ context.Context) error {
				for {
					if _, _, err := connection.Read(context.Background()); err != nil {
						closes <- websocket.CloseStatus(err)
						return nil
					}
				}
			})).To(Succeed())
		}

		stop()
		Eventually(stopped).WithTimeout(stopBudget).Should(Receive(Succeed()))
		Expect(runner.Wait()).To(Succeed())
		Expect(readers.Wait()).To(Succeed())
		for range sessions {
			Expect(<-closes).To(Equal(websocket.StatusCode(4001)),
				"every open session is sent the going-away close")
		}
		for _, connection := range connections {
			_ = connection.CloseNow()
		}
		Expect(app.ActiveConnections()).To(BeZero())

		Expect(logs.count(drainedMessage, "sessions")).To(Equal(int64(sessions)))
		// Each session's read pump, actor and effect at least.
		Expect(logs.count(joinedMessage, "goroutines_joined")).To(BeNumerically(">=", 3*sessions))
		AddReportEntry("goroutines joined at stop",
			fmt.Sprintf("%d sessions, %d goroutines", sessions, logs.count(joinedMessage, "goroutines_joined")))

		client.CloseIdleConnections()
		Eventually(func() error { return goleak.Find(baseline) }).Should(Succeed(),
			"a goroutine the service started outlived the host's stop")
	})

	It("refuses an upgrade once it has stopped", func(ctx SpecContext) {
		app := newCountService(identity, nil)
		Expect(app.Close(ctx)).To(Succeed())

		request := httptest.NewRequest(http.MethodGet, "/live", nil)
		request.Header.Set("Origin", serviceOrigin)
		request.Header.Set("Sec-WebSocket-Protocol", subprotocol)
		recorder := httptest.NewRecorder()
		app.Handler().ServeHTTP(recorder, request)
		Expect(recorder.Code).To(Equal(http.StatusServiceUnavailable))
	})

	Describe("misuse", func() {
		var app *live.App[countState, *MockIIdentity]

		BeforeEach(func() { app = newCountService(identity, nil) })

		It("refuses a nil scope", func() {
			Expect(app.Start(nil)).To(MatchError(ContainSubstring("App.Start needs the scope")))
		})

		It("refuses a second start and starts nothing for it", func(ctx SpecContext) {
			scope := runtime.NewScope(ctx, "service spec")
			Expect(app.Start(scope)).To(Succeed())
			Expect(app.Start(scope)).To(MatchError(live.ErrServiceStarted))
			Expect(scope.Goroutines()).To(Equal(int64(1)), "only the first start owns a goroutine")
			Expect(scope.Close()).To(Succeed())
		})

		It("refuses a start after Close", func(ctx SpecContext) {
			Expect(app.Close(ctx)).To(Succeed())
			Expect(app.Start(runtime.NewScope(ctx, "service spec"))).To(MatchError(live.ErrServiceStopped))
		})

		It("refuses a start after an earlier host stopped it", func(ctx SpecContext) {
			first := runtime.NewScope(ctx, "first host")
			Expect(app.Start(first)).To(Succeed())
			Expect(first.Close()).To(Succeed())
			Expect(app.Start(runtime.NewScope(ctx, "second host"))).To(MatchError(live.ErrServiceStopped))
		})

		It("accepts Close more than once, and after its host stopped it", func(ctx SpecContext) {
			scope := runtime.NewScope(ctx, "service spec")
			Expect(app.Start(scope)).To(Succeed())
			Expect(scope.Close()).To(Succeed())
			Expect(app.Close(ctx)).To(Succeed())
			Expect(app.Close(ctx)).To(Succeed())
		})
	})
})
