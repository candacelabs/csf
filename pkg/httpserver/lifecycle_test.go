package httpserver_test

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	sharedhttp "github.com/candacelabs/csf/pkg/httpserver"
)

const streamShutdownBudget = 5 * time.Second

var _ = Describe("The shared HTTP lifecycle", func() {
	It("reports a shutdown deadline and closes connections when a handler ignores cancellation", func(ctx SpecContext) {
		const shutdownBudget = 20 * time.Second
		addresses := make(chan string, 1)
		started, completed := make(chan struct{}), make(chan struct{})
		releaseContext, releaseHandler := context.WithCancel(context.Background())
		DeferCleanup(releaseHandler)
		server := &http.Server{
			Addr: "127.0.0.1:0",
			BaseContext: func(listener net.Listener) context.Context {
				addresses <- listener.Addr().String()
				return context.Background()
			},
			Handler: http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				close(started)
				// The separate stream test covers cooperative cancellation. This
				// handler deliberately stays active so Shutdown must time out.
				<-releaseContext.Done()
				close(completed)
			}),
		}
		DeferCleanup(server.Close)
		runCtx, cancel := context.WithCancel(ctx)
		DeferCleanup(cancel)
		stopped := make(chan error, 1)
		go func() { stopped <- sharedhttp.Serve(runCtx, server) }()
		var address string
		Eventually(addresses, shutdownBudget).Should(Receive(&address))
		requested := make(chan error, 1)
		go func() { requested <- sharedhttp.Probe(ctx, "http://"+address) }()
		Eventually(started, shutdownBudget).Should(BeClosed())
		cancel()
		Eventually(stopped, shutdownBudget).Should(Receive(MatchError(ContainSubstring("shutdown HTTP server: context deadline exceeded"))))
		Eventually(requested, shutdownBudget).Should(Receive(HaveOccurred()))
		Expect(completed).NotTo(BeClosed())
		releaseHandler()
		Eventually(completed, shutdownBudget).Should(BeClosed())
	})

	It("rejects missing lifecycle inputs", func() {
		Expect(sharedhttp.Serve(nil, &http.Server{})).To(MatchError("httpserver: context is nil"))
		Expect(sharedhttp.Serve(context.Background(), nil)).To(MatchError("httpserver: server is nil"))
		Expect(sharedhttp.Probe(nil, "http://example.invalid")).To(MatchError("httpserver: context is nil"))
		Expect(sharedhttp.Probe(context.Background(), "://invalid")).To(MatchError(ContainSubstring("create health request")))
	})

	It("requires a successful health response", func(ctx SpecContext) {
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			switch request.URL.Path {
			case "/ok":
				writer.WriteHeader(http.StatusNoContent)
			case "/redirect":
				http.Redirect(writer, request, "/ok", http.StatusTemporaryRedirect)
			default:
				http.Error(writer, "unhealthy", http.StatusServiceUnavailable)
			}
		}))
		DeferCleanup(server.Close)

		Expect(sharedhttp.Probe(ctx, server.URL+"/ok")).To(Succeed())
		Expect(sharedhttp.Probe(ctx, server.URL+"/bad")).To(MatchError(ContainSubstring("503")))
		Expect(sharedhttp.Probe(ctx, server.URL+"/redirect")).To(MatchError(ContainSubstring("307")))
	})

	It("serves until cancellation and drains the listener", func(ctx SpecContext) {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		Expect(err).NotTo(HaveOccurred())
		address := listener.Addr().String()
		Expect(listener.Close()).To(Succeed())

		runContext, cancel := context.WithCancel(context.Background())
		server := &http.Server{
			Addr: address,
			Handler: http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				writer.WriteHeader(http.StatusNoContent)
			}),
		}
		stopped := make(chan error, 1)
		go func() { stopped <- sharedhttp.Serve(runContext, server) }()

		Eventually(func() error { return sharedhttp.Probe(ctx, "http://"+address) }).Should(Succeed())
		cancel()
		Eventually(stopped).Should(Receive(Succeed()))
	})

	It("cancels an open event stream before graceful shutdown", func(ctx SpecContext) {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		Expect(err).NotTo(HaveOccurred())
		address := listener.Addr().String()
		Expect(listener.Close()).To(Succeed())

		streamOpened := make(chan struct{})
		streamCanceled := make(chan struct{})
		baseValue := make(chan string, 1)
		type baseContextKey struct{}
		handler := http.NewServeMux()
		handler.HandleFunc("/ready", func(writer http.ResponseWriter, _ *http.Request) {
			writer.WriteHeader(http.StatusNoContent)
		})
		handler.HandleFunc("/events", func(writer http.ResponseWriter, request *http.Request) {
			baseValue <- request.Context().Value(baseContextKey{}).(string)
			writer.Header().Set("Content-Type", "text/event-stream")
			writer.WriteHeader(http.StatusOK)
			writer.(http.Flusher).Flush()
			close(streamOpened)
			<-request.Context().Done()
			close(streamCanceled)
		})
		server := sharedhttp.NewStreamingServer(address, handler)
		server.BaseContext = func(_ net.Listener) context.Context {
			return context.WithValue(context.Background(), baseContextKey{}, "preserved")
		}
		runContext, cancel := context.WithCancel(context.Background())
		stopped := make(chan error, 1)
		go func() { stopped <- sharedhttp.Serve(runContext, server) }()
		DeferCleanup(func() {
			cancel()
			_ = server.Close()
		})

		Eventually(func() error {
			return sharedhttp.Probe(ctx, "http://"+address+"/ready")
		}).WithTimeout(streamShutdownBudget).Should(Succeed())
		response, err := http.Get("http://" + address + "/events")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(response.Body.Close)
		Expect(response.StatusCode).To(Equal(http.StatusOK))
		Expect(baseValue).To(Receive(Equal("preserved")))
		Eventually(streamOpened).WithTimeout(streamShutdownBudget).Should(BeClosed())

		cancel()

		Eventually(streamCanceled).WithTimeout(streamShutdownBudget).Should(BeClosed())
		Eventually(stopped).WithTimeout(streamShutdownBudget).Should(Receive(Succeed()))
	})

	It("reports a bind failure with lifecycle context", func() {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(listener.Close)

		server := &http.Server{Addr: listener.Addr().String()}
		baseContext := context.WithValue(context.Background(), struct{}{}, "original")
		server.BaseContext = func(_ net.Listener) context.Context { return baseContext }

		Expect(sharedhttp.Serve(context.Background(), server)).To(MatchError(And(
			ContainSubstring("serve HTTP"),
			ContainSubstring("address already in use"),
		)))
		Expect(server.BaseContext(listener)).To(BeIdenticalTo(baseContext))
	})
})
