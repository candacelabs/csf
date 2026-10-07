// Copyright 2026 Candace Labs

package http_test

import (
	"context"
	"io"
	stdnet "net"
	stdhttp "net/http"
	"os"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/goleak"

	ionet "github.com/candacelabs/csf/io/net"
	iohttp "github.com/candacelabs/csf/io/net/http"
	"github.com/candacelabs/csf/pkg/eventually"
	"github.com/candacelabs/csf/runtime"
)

const (
	socketName    = "csf.sock"
	notSocket     = "not a socket"
	unixNetwork   = "unix"
	unixBase      = "http://unix"
	hungPath      = "/hung"
	shortBudget   = 200 * time.Millisecond
	stopAllowance = 3 * time.Second
	fileMode      = 0o600
)

// unixClient speaks HTTP over the unix socket at path.
func unixClient(path string) *stdhttp.Client {
	return &stdhttp.Client{Transport: &stdhttp.Transport{
		DialContext: func(ctx context.Context, _ string, _ string) (stdnet.Conn, error) {
			var dialer stdnet.Dialer
			return dialer.DialContext(ctx, unixNetwork, path)
		},
	}}
}

// runHost mounts listener alone and runs it until the returned stop is called;
// stop reports how long the host took to return after cancellation.
func runHost(listener runtime.IService) (stop func() (time.Duration, error), started error) {
	GinkgoHelper()
	host, err := runtime.NewHostRuntime()
	Expect(err).NotTo(HaveOccurred())
	Expect(host.Mount("http", listener)).To(Succeed())
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- host.Run(ctx) }()
	ready := eventually.Await(GinkgoTB(), "the listener to start or fail", serveBudget,
		func() bool {
			select {
			case err := <-done:
				done <- err
				return true
			default:
				return host.Ready()
			}
		}, func(settled bool) bool { return settled })
	Expect(ready).To(BeTrue())
	select {
	case err := <-done:
		cancel()
		return nil, err
	default:
	}
	return func() (time.Duration, error) {
		began := time.Now()
		cancel()
		err := <-done
		return time.Since(began), err
	}, nil
}

var _ = Describe("HTTPListener lifecycle", func() {
	var baseline goleak.Option

	BeforeEach(func() { baseline = goleak.IgnoreCurrent() })
	AfterEach(func() {
		stdhttp.DefaultClient.CloseIdleConnections()
		Expect(goleak.Find(baseline)).To(Succeed())
	})

	It("replaces a stale unix socket a crashed process left behind", func() {
		path := filepath.Join(GinkgoT().TempDir(), socketName)
		crashed, err := stdnet.ListenUnix(unixNetwork, &stdnet.UnixAddr{Name: path, Net: unixNetwork})
		Expect(err).NotTo(HaveOccurred())
		crashed.SetUnlinkOnClose(false)
		Expect(crashed.Close()).To(Succeed())
		Expect(path).To(BeAnExistingFile(), "the fixture must leave a stale socket file")

		listener, err := iohttp.NewUnixSocketHTTPListener(ionet.NewHostNetwork(), path, handler(make(chan struct{}, 1)))
		Expect(err).NotTo(HaveOccurred())
		stop, err := runHost(listener)
		Expect(err).NotTo(HaveOccurred())
		client := unixClient(path)
		response, err := client.Get(unixBase + pathPlain)
		Expect(err).NotTo(HaveOccurred())
		body, err := io.ReadAll(response.Body)
		Expect(response.Body.Close()).To(Succeed())
		Expect(err).NotTo(HaveOccurred())
		Expect(string(body)).To(Equal(responseBody))
		client.CloseIdleConnections()
		_, err = stop()
		Expect(err).NotTo(HaveOccurred())
	})

	It("leaves a unix socket that still answers alone", func() {
		path := filepath.Join(GinkgoT().TempDir(), socketName)
		live, err := stdnet.ListenUnix(unixNetwork, &stdnet.UnixAddr{Name: path, Net: unixNetwork})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(live.Close)

		listener, err := iohttp.NewUnixSocketHTTPListener(ionet.NewHostNetwork(), path, handler(make(chan struct{}, 1)))
		Expect(err).NotTo(HaveOccurred())
		_, err = runHost(listener)
		Expect(err).To(MatchError(ContainSubstring("ipc/net: listen unix")))
		connection, err := stdnet.Dial(unixNetwork, path)
		Expect(err).NotTo(HaveOccurred(), "the live socket must survive")
		Expect(connection.Close()).To(Succeed())
	})

	It("never removes a path that is not a socket", func() {
		path := filepath.Join(GinkgoT().TempDir(), socketName)
		Expect(os.WriteFile(path, []byte(notSocket), fileMode)).To(Succeed())

		listener, err := iohttp.NewUnixSocketHTTPListener(ionet.NewHostNetwork(), path, handler(make(chan struct{}, 1)))
		Expect(err).NotTo(HaveOccurred())
		_, err = runHost(listener)
		Expect(err).To(HaveOccurred())
		Expect(os.ReadFile(path)).To(Equal([]byte(notSocket)))
	})

	It("cuts a handler that ignores cancellation off at its shutdown budget", func() {
		release := make(chan struct{})
		entered := make(chan struct{}, 1)
		mux := stdhttp.NewServeMux()
		mux.HandleFunc(hungPath, func(writer stdhttp.ResponseWriter, _ *stdhttp.Request) {
			writer.WriteHeader(stdhttp.StatusOK)
			writer.(stdhttp.Flusher).Flush()
			entered <- struct{}{}
			<-release
		})
		listener, err := iohttp.NewHTTPListener(ionet.NewHostNetwork(), loopback, mux, iohttp.WithShutdownBudget(shortBudget))
		Expect(err).NotTo(HaveOccurred())
		stop, err := runHost(listener)
		Expect(err).NotTo(HaveOccurred())
		response, err := stdhttp.Get("http://" + listener.Addr().String() + hungPath)
		Expect(err).NotTo(HaveOccurred())
		Eventually(entered).WithTimeout(serveBudget.Within).Should(Receive())

		took, err := stop()
		close(release)
		Expect(response.Body.Close()).To(Succeed())
		Expect(err).To(MatchError(ContainSubstring("shutdown HTTP server")), "an expired drain is reported, not hidden")
		Expect(took).To(BeNumerically("<", shortBudget+stopAllowance))
		Expect(took).To(BeNumerically(">=", shortBudget))
	})

	It("releases the bound socket when its scope refuses the serving goroutine", func(ctx SpecContext) {
		scope := runtime.NewScope(ctx, "already closed")
		Expect(scope.Close()).To(Succeed())
		network := ionet.NewHostNetwork()
		probe, err := network.Listen(ctx, "tcp", loopback)
		Expect(err).NotTo(HaveOccurred())
		address := probe.Addr().String()
		Expect(probe.Close()).To(Succeed())

		listener, err := iohttp.NewHTTPListener(network, address, stdhttp.NotFoundHandler())
		Expect(err).NotTo(HaveOccurred())
		Expect(listener.Start(scope)).To(MatchError(runtime.ErrScopeClosed))
		again, err := network.Listen(ctx, "tcp", address)
		Expect(err).NotTo(HaveOccurred(), "a refused start must not keep the port")
		Expect(again.Close()).To(Succeed())
	})

	It("refuses a second Start of a listener that is already serving", func() {
		listener, err := iohttp.NewHTTPListener(ionet.NewHostNetwork(), loopback, stdhttp.NotFoundHandler())
		Expect(err).NotTo(HaveOccurred())
		scope := runtime.NewScope(context.Background(), "double start")
		Expect(listener.Start(scope)).To(Succeed())
		Expect(listener.Start(scope)).To(MatchError(ContainSubstring("already started")))
		Expect(scope.Close()).To(Succeed())
	})

	It("refuses a non-positive shutdown budget", func() {
		_, err := iohttp.NewHTTPListener(ionet.NewHostNetwork(), loopback, stdhttp.NotFoundHandler(), iohttp.WithShutdownBudget(0))
		Expect(err).To(MatchError(ContainSubstring("shutdown budget")))
	})
})
