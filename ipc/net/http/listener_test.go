// Copyright 2026 Candace Labs

package http_test

import (
	"context"
	"io"
	stdhttp "net/http"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/goleak"

	ipcnet "github.com/candacelabs/csf/ipc/net"
	ipchttp "github.com/candacelabs/csf/ipc/net/http"
	"github.com/candacelabs/csf/pkg/eventually"
	"github.com/candacelabs/csf/runtime"
)

const (
	loopback     = "127.0.0.1:0"
	pathPlain    = "/plain"
	pathStream   = "/stream"
	responseBody = "served"
)

var serveBudget = eventually.Budget{Within: 10 * time.Second}

// handler answers one ordinary request and holds one streaming request open
// until the request context ends, the way an SSE or WebSocket handler does.
func handler(streaming chan<- struct{}) stdhttp.Handler {
	mux := stdhttp.NewServeMux()
	mux.HandleFunc(pathPlain, func(writer stdhttp.ResponseWriter, _ *stdhttp.Request) {
		_, _ = io.WriteString(writer, responseBody)
	})
	mux.HandleFunc(pathStream, func(writer stdhttp.ResponseWriter, request *stdhttp.Request) {
		writer.WriteHeader(stdhttp.StatusOK)
		writer.(stdhttp.Flusher).Flush()
		streaming <- struct{}{}
		<-request.Context().Done()
	})
	return mux
}

var _ = Describe("HTTPListener", func() {
	var baseline goleak.Option

	BeforeEach(func() { baseline = goleak.IgnoreCurrent() })
	AfterEach(func() {
		stdhttp.DefaultClient.CloseIdleConnections()
		Expect(goleak.Find(baseline)).To(Succeed())
	})

	It("serves from a runtime scope and drains open streams on shutdown", func() {
		streaming := make(chan struct{}, 1)
		listener, err := ipchttp.NewHTTPListener(ipcnet.NewHostNetwork(), loopback, handler(streaming))
		Expect(err).NotTo(HaveOccurred())
		Expect(listener.Addr()).To(BeNil())
		host, err := runtime.NewHostRuntime()
		Expect(err).NotTo(HaveOccurred())
		Expect(host.Mount("http", listener)).To(Succeed())

		ctx, stop := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() { done <- host.Run(ctx) }()
		eventually.Await(GinkgoTB(), "the listener to start", serveBudget, host.Ready, func(ready bool) bool { return ready })
		base := "http://" + listener.Addr().String()

		response, err := stdhttp.Get(base + pathPlain)
		Expect(err).NotTo(HaveOccurred())
		body, err := io.ReadAll(response.Body)
		Expect(response.Body.Close()).To(Succeed())
		Expect(err).NotTo(HaveOccurred())
		Expect(string(body)).To(Equal(responseBody))

		stream, err := stdhttp.Get(base + pathStream)
		Expect(err).NotTo(HaveOccurred())
		Eventually(streaming).WithTimeout(serveBudget.Within).Should(Receive())

		stop()
		Eventually(done).WithTimeout(serveBudget.Within).Should(Receive(Succeed()))
		_, _ = io.Copy(io.Discard, stream.Body)
		Expect(stream.Body.Close()).To(Succeed())
	})

	It("fails startup when the port is already bound", func(ctx SpecContext) {
		network := ipcnet.NewHostNetwork()
		occupied, err := network.Listen(ctx, "tcp", loopback)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(occupied.Close)

		listener, err := ipchttp.NewHTTPListener(network, occupied.Addr().String(), handler(make(chan struct{}, 1)))
		Expect(err).NotTo(HaveOccurred())
		host, err := runtime.NewHostRuntime()
		Expect(err).NotTo(HaveOccurred())
		Expect(host.Mount("http", listener)).To(Succeed())
		Expect(host.Run(ctx)).To(MatchError(ContainSubstring("ipc/net: listen tcp")))
	})

	It("refuses an incomplete grant", func() {
		_, err := ipchttp.NewHTTPListener(nil, loopback, stdhttp.NotFoundHandler())
		Expect(err).To(HaveOccurred())
		_, err = ipchttp.NewHTTPListener(ipcnet.NewHostNetwork(), loopback, nil)
		Expect(err).To(HaveOccurred())
		_, err = ipchttp.NewHTTPListener(ipcnet.NewHostNetwork(), "no-port", stdhttp.NotFoundHandler())
		Expect(err).To(HaveOccurred())
	})
})
