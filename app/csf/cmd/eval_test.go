package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/io/net/model/jev"
)

var _ = Describe("csf eval", func() {
	Describe("an interrupted run", func() {
		It("measures nothing once the context it was given is cancelled", func() {
			var requests atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				requests.Add(1)
			}))
			DeferCleanup(server.Close)
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			err := eval(ctx, []string{"--endpoint", server.URL, "--model", jev.DefaultDeciderModel.Name}, io.Discard)
			Expect(err).To(MatchError("eval measured no declared model"))
			Expect(requests.Load()).To(BeZero())
		})
	})
})
