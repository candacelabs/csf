// Copyright 2026 Candace Labs

package adaptertest_test

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/services/copilot-adapter/adaptertest"
)

const (
	// concurrentClients keep several keep-alive connections busy at once: the
	// unfixed in-memory connection only misbehaved when the scheduler had other
	// goroutines to run between a response and the next request.
	concurrentClients = 16
	// requestsPerClient is how many back-to-back requests each client sends
	// over its one connection.
	requestsPerClient = 400
	// servers is how many independent servers, each with fresh connections,
	// the spec runs the clients against: whether the unfixed connection
	// misbehaved in one server was a matter of how its goroutines fell into
	// step, so one server missed it about one run in two.
	servers = 5
	// observationWindows spreads the time a handler holds a request open, in
	// microseconds, so the spec never settles into one rhythm.
	observationWindows = 200
)

var _ = Describe("Serve", func() {
	It("never cancels the context of a request it is still serving", func() {
		var served, canceled atomic.Int64
		handler := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			_, _ = io.Copy(io.Discard, request.Body)
			sequence := served.Add(1)
			// The wait is the observation window of this spec, not a wait for
			// an event: a correct server never ends the context inside it, so
			// the only thing that can end the wait early is the defect (CS-9
			// counterweight 1).
			window := time.Duration(sequence*73%observationWindows) * time.Microsecond
			select {
			case <-request.Context().Done():
				canceled.Add(1)
				writer.WriteHeader(http.StatusInternalServerError)
				return
			case <-time.After(window):
			}
			if sequence%2 == 0 {
				writer.WriteHeader(http.StatusNoContent)
				return
			}
			_, _ = writer.Write([]byte("ok"))
		})

		for range servers {
			server := adaptertest.Serve(GinkgoT(), handler)
			failures := make(chan error, concurrentClients)
			var clients sync.WaitGroup
			for range concurrentClients {
				clients.Add(1)
				go func() {
					defer clients.Done()
					for count := range requestsPerClient {
						if err := send(server, count); err != nil {
							failures <- fmt.Errorf("request %d: %w", count, err)
							return
						}
					}
				}()
			}
			clients.Wait()
			close(failures)
			for err := range failures {
				Expect(err).NotTo(HaveOccurred())
			}
		}

		Expect(served.Load()).To(Equal(int64(servers*concurrentClients*requestsPerClient)), "every request reached the handler")
		Expect(canceled.Load()).To(BeZero(), "requests whose context ended while the handler still held them")
	})
})

// send is one request over the server's keep-alive connection, alternating the
// two ways net/http starts watching a connection for a close: at once for a
// request without a body, and once the body is read for one with.
func send(server *adaptertest.Server, count int) error {
	var response *http.Response
	var err error
	if count%2 == 0 {
		response, err = server.Client.Get(server.URL)
	} else {
		response, err = server.Client.Post(server.URL, "application/json", strings.NewReader(`{"text":"hello"}`))
	}
	if err != nil {
		return err
	}
	defer response.Body.Close()
	_, err = io.Copy(io.Discard, response.Body)
	return err
}
