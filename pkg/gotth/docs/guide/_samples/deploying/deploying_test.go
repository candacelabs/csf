package deploying_test

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/pkg/gotth/docs/guide/_samples/deploying"
	"github.com/candacelabs/csf/pkg/gotth/live"
)

// These specs are the executable half of docs/guide/deploying.md. Every claim
// the page makes about a knob, a status code or an ordering is asserted here,
// because a deployment page whose advice has never been run is a page that
// tells an operator to do something the library refuses.

var origins = []string{"https://app.example.com"}

var _ = Describe("Production", func() {
	It("leaves Dev off, which is the only setting on this page that is not a trade-off", func() {
		cfg := deploying.Production(deploying.Config(origins), 30*time.Second, 5000)

		Expect(cfg.Dev).To(BeFalse())
		Expect(cfg.DevBuildID).To(BeEmpty())
	})

	It("produces a Config live.New accepts, across the idle timeouts a real path has", func() {
		// One entry per idle timeout an operator actually meets: an AWS ALB's
		// 60 s default, a Kubernetes ingress-nginx 60 s, a Cloudflare 100 s,
		// a conservative 30 s, and a very long one that has to be clamped
		// against the protocol's five-minute ceiling.
		for _, proxyIdle := range []time.Duration{
			30 * time.Second,
			60 * time.Second,
			100 * time.Second,
			10 * time.Minute,
			2 * time.Hour,
		} {
			cfg := deploying.Production(deploying.Config(origins), proxyIdle, 5000)

			app, err := live.New(cfg)
			Expect(err).NotTo(HaveOccurred(), "an idle timeout of %s produced a Config live.New refuses", proxyIdle)
			Expect(app).NotTo(BeNil())

			Expect(cfg.Limits.HeartbeatInterval).To(BeNumerically("<=", deploying.MaxHeartbeatInterval))
			Expect(cfg.Limits.HeartbeatInterval).To(BeNumerically("<", proxyIdle),
				"the heartbeat must be shorter than the shortest idle timeout in the path")
			Expect(cfg.Limits.HeartbeatTimeout).To(BeNumerically(">=", 2*cfg.Limits.HeartbeatInterval),
				"live.New refuses a timeout below two intervals")
		}
	})

	It("sets the process bound, because Limits.MaxSessions defaults to unlimited", func() {
		Expect(live.DefaultLimits().MaxSessions).To(Equal(0))

		cfg := deploying.Production(deploying.Config(origins), 30*time.Second, 5000)
		Expect(cfg.Limits.MaxSessions).To(Equal(5000))
	})

	It("is refused by live.New, naming the field, when the path idles out in under three seconds", func() {
		cfg := deploying.Production(deploying.Config(origins), 2*time.Second, 5000)

		_, err := live.New(cfg)
		Expect(err).To(HaveOccurred())

		var cfgErr *live.ConfigError
		Expect(errors.As(err, &cfgErr)).To(BeTrue(), "live.New returned %T, not a *live.ConfigError", err)
		Expect(cfgErr.Field).To(Equal("Limits.HeartbeatInterval"))
	})
})

var _ = Describe("the client runtime the binary serves", func() {
	It("is served from the mount with a strong ETag and a year of immutable caching", func() {
		app, err := live.New(deploying.Config(origins))
		Expect(err).NotTo(HaveOccurred())

		server := httptest.NewServer(app.Handler())
		DeferCleanup(server.Close)

		resp, err := http.Get(server.URL + "/gotth-live.min.js")
		Expect(err).NotTo(HaveOccurred())
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		Expect(err).NotTo(HaveOccurred())

		Expect(resp.StatusCode).To(Equal(http.StatusOK))
		Expect(body).NotTo(BeEmpty())
		Expect(resp.Header.Get("Content-Type")).To(Equal("text/javascript; charset=utf-8"))
		Expect(resp.Header.Get("X-Content-Type-Options")).To(Equal("nosniff"))
		Expect(resp.Header.Get("ETag")).NotTo(BeEmpty())

		// The page states this header verbatim, and the consequence it has for
		// an upgrade: the URL carries no fingerprint, so a browser holding this
		// response will not revalidate it for a year.
		Expect(resp.Header.Get("Cache-Control")).To(Equal("public, max-age=31536000, immutable"))
	})

	It("answers a conditional request with 304, so a reconnecting page refetches nothing", func() {
		app, err := live.New(deploying.Config(origins))
		Expect(err).NotTo(HaveOccurred())

		server := httptest.NewServer(app.Handler())
		DeferCleanup(server.Close)

		first, err := http.Get(server.URL + "/gotth-live.min.js")
		Expect(err).NotTo(HaveOccurred())
		defer first.Body.Close()
		_, _ = io.Copy(io.Discard, first.Body)

		req, err := http.NewRequest(http.MethodGet, server.URL+"/gotth-live.min.js", nil)
		Expect(err).NotTo(HaveOccurred())
		req.Header.Set("If-None-Match", first.Header.Get("ETag"))

		second, err := http.DefaultClient.Do(req)
		Expect(err).NotTo(HaveOccurred())
		defer second.Body.Close()
		_, _ = io.Copy(io.Discard, second.Body)

		Expect(second.StatusCode).To(Equal(http.StatusNotModified))
	})
})

var _ = Describe("Readiness", func() {
	It("answers 200 until it is drained and 503 afterwards", func() {
		ready := &deploying.Readiness{}

		rec := httptest.NewRecorder()
		ready.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
		Expect(rec.Code).To(Equal(http.StatusOK))
		Expect(rec.Header().Get("Cache-Control")).To(Equal("no-store"))

		ready.Drain()

		rec = httptest.NewRecorder()
		ready.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
		Expect(rec.Code).To(Equal(http.StatusServiceUnavailable))
	})
})

var _ = Describe("Deployment.Run", func() {
	It("fails readiness before it drains the sessions, and drains them exactly once", func() {
		ready := &deploying.Readiness{}

		var (
			drains            int
			readyWhenDraining bool
		)

		listener, err := net.Listen("tcp", "127.0.0.1:0")
		Expect(err).NotTo(HaveOccurred())

		d := &deploying.Deployment{
			Server:   &http.Server{Handler: ready, ReadHeaderTimeout: 5 * time.Second},
			Listener: listener,
			Ready:    ready,
			DrainSessions: func(context.Context) error {
				drains++
				// Read the flag at the moment the sessions are being drained.
				// If readiness were failed after this point, a load balancer
				// could still be routing new upgrades at a process that is
				// closing the sessions it already has.
				readyWhenDraining = ready.Draining()
				return nil
			},
			Grace: 5 * time.Second,
		}

		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() { done <- d.Run(ctx) }()

		// Wait until the listener is actually answering before asking for the
		// shutdown, so the assertion is about the ordering and not about a
		// race with Serve.
		Eventually(func() int {
			resp, err := http.Get("http://" + listener.Addr().String() + "/readyz")
			if err != nil {
				return 0
			}
			defer resp.Body.Close()
			return resp.StatusCode
		}).Should(Equal(http.StatusOK))

		cancel()

		Eventually(done).Should(Receive(BeNil()))
		Expect(drains).To(Equal(1))
		Expect(readyWhenDraining).To(BeTrue(),
			"the sessions were drained while readiness was still answering 200")
	})

	It("returns the server's error when it stops on its own, rather than draining", func() {
		ready := &deploying.Readiness{}

		listener, err := net.Listen("tcp", "127.0.0.1:0")
		Expect(err).NotTo(HaveOccurred())
		// Close the listener out from under Serve, so Serve returns an error
		// that is not http.ErrServerClosed.
		Expect(listener.Close()).To(Succeed())

		d := &deploying.Deployment{
			Server:        &http.Server{Handler: ready, ReadHeaderTimeout: 5 * time.Second},
			Listener:      listener,
			Ready:         ready,
			DrainSessions: func(context.Context) error { return errors.New("must not be reached") },
			Grace:         time.Second,
		}

		Expect(d.Run(context.Background())).To(HaveOccurred())
		Expect(ready.Draining()).To(BeFalse())
	})
})
