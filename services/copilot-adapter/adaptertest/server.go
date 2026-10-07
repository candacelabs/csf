package adaptertest

import (
	"context"
	stdnet "net"
	"net/http"
	"time"

	"go.uber.org/mock/gomock"

	iohttp "github.com/candacelabs/csf/io/net/http"
	"github.com/candacelabs/csf/runtime"
)

//go:generate go run go.uber.org/mock/mockgen@v0.6.0 -destination=mock_network.go -package=adaptertest github.com/candacelabs/csf/io/net IListener,IDialer

const (
	// serverAddress is what the listener binds and the client dials; no
	// resolver ever sees it.
	serverAddress = "copilot-adapter.invalid:80"
	serverURL     = "http://" + serverAddress
	// shutdownBudget bounds the drain of a stream a spec left open.
	shutdownBudget = time.Second
	tcp            = "tcp"
)

// Server is a handler served the way the binary serves it — by the
// ipc/net/http listener, mounted in a runtime scope — over an in-memory
// connection (see memoryListener) that a gomock socket capability hands to
// both ends.
type Server struct {
	// URL is the base URL Client reaches the handler at.
	URL string
	// Client dials through the ipc/net/http client capability.
	Client *http.Client
}

// Serve starts handler for one spec and stops it, joining every serving
// goroutine, when the spec ends.
func Serve(reporter IReporter, handler http.Handler) *Server {
	reporter.Helper()
	memory := newMemoryListener()
	controller := gomock.NewController(reporter)
	network := NewMockIListener(controller)
	network.EXPECT().Listen(gomock.Any(), tcp, serverAddress).Return(memory, nil)
	dialer := NewMockIDialer(controller)
	dialer.EXPECT().DialContext(gomock.Any(), tcp, serverAddress).DoAndReturn(
		func(ctx context.Context, _ string, _ string) (stdnet.Conn, error) { return memory.DialContext(ctx) }).AnyTimes()
	listener, err := iohttp.NewHTTPListener(network, serverAddress, handler, iohttp.WithShutdownBudget(shutdownBudget))
	if err != nil {
		reporter.Fatalf("adaptertest: listener: %v", err)
	}
	client, err := iohttp.NewHTTPClient(dialer)
	if err != nil {
		reporter.Fatalf("adaptertest: client: %v", err)
	}
	scope := runtime.NewScope(context.Background(), "adaptertest http")
	if err := listener.Start(scope); err != nil {
		reporter.Fatalf("adaptertest: start: %v", err)
	}
	reporter.Cleanup(func() {
		client.CloseIdleConnections()
		if err := scope.Close(); err != nil {
			reporter.Errorf("adaptertest: stop: %v", err)
		}
	})
	return &Server{URL: serverURL, Client: client}
}
