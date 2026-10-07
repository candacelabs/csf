// Copyright 2026 Candace Labs

package http_test

import (
	"bufio"
	"context"
	"errors"
	"io"
	stdnet "net"
	stdhttp "net/http"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/goleak"
	"go.uber.org/mock/gomock"

	iohttp "github.com/candacelabs/csf/io/net/http"
	"github.com/candacelabs/csf/runtime"
)

const (
	apiAddress = "api.example.invalid:80"
	apiURL     = "http://api.example.invalid/plain"
)

// errRefused is what the granted dialer answers when a spec denies the grant.
var errRefused = errors.New("dial refused by the grant")

// answerOnce serves exactly one HTTP exchange on the server end of an
// in-memory pipe, from a scope the spec owns: no socket, no kernel.
func answerOnce(scope *runtime.Scope, server stdnet.Conn) {
	Expect(scope.Go(func(_ context.Context) error {
		defer server.Close()
		request, err := stdhttp.ReadRequest(bufio.NewReader(server))
		if err != nil {
			return err
		}
		response := &stdhttp.Response{
			StatusCode: stdhttp.StatusOK, ProtoMajor: 1, ProtoMinor: 1, Request: request,
			Header:        stdhttp.Header{},
			ContentLength: int64(len(responseBody)),
			Body:          io.NopCloser(strings.NewReader(responseBody)),
			Close:         true,
		}
		return response.Write(server)
	})).To(Succeed())
}

var _ = Describe("NewHTTPClient", func() {
	BeforeEach(func() {
		baseline := goleak.IgnoreCurrent()
		// Registered first, so it runs after every cleanup a spec defers.
		DeferCleanup(func() { Expect(goleak.Find(baseline)).To(Succeed()) })
	})

	It("dials every connection through the granted capability", func(ctx SpecContext) {
		controller := gomock.NewController(GinkgoT())
		dialer := NewMockIDialer(controller)
		client, server := stdnet.Pipe()
		scope := runtime.NewScope(ctx, "fake api")
		DeferCleanup(func() { Expect(scope.Close()).To(Succeed()) })
		dialer.EXPECT().DialContext(gomock.Any(), "tcp", apiAddress).DoAndReturn(
			func(_ context.Context, _ string, _ string) (stdnet.Conn, error) {
				answerOnce(scope, server)
				return client, nil
			}).Times(1)

		httpClient, err := iohttp.NewHTTPClient(dialer)
		Expect(err).NotTo(HaveOccurred())
		var _ iohttp.IHTTPClient = httpClient
		DeferCleanup(httpClient.CloseIdleConnections)

		request, err := stdhttp.NewRequestWithContext(ctx, stdhttp.MethodGet, apiURL, nil)
		Expect(err).NotTo(HaveOccurred())
		response, err := httpClient.Do(request)
		Expect(err).NotTo(HaveOccurred())
		body, err := io.ReadAll(response.Body)
		Expect(response.Body.Close()).To(Succeed())
		Expect(err).NotTo(HaveOccurred())
		Expect(string(body)).To(Equal(responseBody))
	})

	It("reports the grant's refusal rather than dialing on its own", func(ctx SpecContext) {
		controller := gomock.NewController(GinkgoT())
		dialer := NewMockIDialer(controller)
		dialer.EXPECT().DialContext(gomock.Any(), "tcp", apiAddress).Return(nil, errRefused).Times(1)
		httpClient, err := iohttp.NewHTTPClient(dialer)
		Expect(err).NotTo(HaveOccurred())

		request, err := stdhttp.NewRequestWithContext(ctx, stdhttp.MethodGet, apiURL, nil)
		Expect(err).NotTo(HaveOccurred())
		_, err = httpClient.Do(request)
		Expect(err).To(MatchError(errRefused))
	})

	It("refuses an incomplete grant", func() {
		_, err := iohttp.NewHTTPClient(nil)
		Expect(err).To(MatchError(ContainSubstring("a dialer capability is required")))
		controller := gomock.NewController(GinkgoT())
		_, err = iohttp.NewHTTPClient(NewMockIDialer(controller), iohttp.WithClientTimeout(0))
		Expect(err).To(MatchError(ContainSubstring("client timeout must be positive")))
	})
})
