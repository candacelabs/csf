// Copyright 2026 Candace Labs

package verbs

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/io/net/model/jev"
)

var _ = Describe("csf eval score", func() {
	logger := slog.New(slog.DiscardHandler)

	Describe("models", func() {
		It("measures nothing once the context it was given is cancelled", func() {
			var requests atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				requests.Add(1)
			}))
			DeferCleanup(server.Close)
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			err := evalScore(ctx, nil, []string{scoreModels, "-endpoint", server.URL, "-model", jev.DefaultDeciderModel.Name}, io.Discard, logger)
			Expect(err).To(MatchError(jev.ErrNoMeasuredModel))
			Expect(requests.Load()).To(BeZero())
		})

		It("refuses a model that is not declared", func() {
			err := evalScore(context.Background(), nil, []string{scoreModels, "-model", "undeclared:0b"}, io.Discard, logger)
			Expect(err).To(HaveOccurred())
		})
	})

	It("refuses a candidate kind it has no evaluator for", func() {
		var output bytes.Buffer
		err := evalScore(context.Background(), nil, []string{"tickets"}, &output, logger)
		Expect(err).To(MatchError(errUsageEval))
		Expect(output.String()).To(BeEmpty())
	})

	It("registers one evaluation per candidate kind", func() {
		Expect(scoreKinds).To(HaveKey(scoreBuilds))
		Expect(scoreKinds).To(HaveKey(scoreModels))
		Expect(scoreKinds).To(HaveLen(2))
	})
})
