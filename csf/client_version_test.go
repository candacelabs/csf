// Copyright 2026 Candace Labs

package csf_test

import (
	"context"
	"io"
	"net/http"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/csf"
	harnessv1 "github.com/candacelabs/csf/proto/candace/harness/v1"
)

// cannedHost answers every request with one body and the version header a
// newer host sends.
type cannedHost struct {
	body    string
	version string
}

func (host cannedHost) Do(request *http.Request) (*http.Response, error) {
	header := http.Header{}
	if host.version != "" {
		header.Set(csf.VersionHeader, host.version)
	}
	return &http.Response{StatusCode: http.StatusOK, Header: header, Body: io.NopCloser(strings.NewReader(host.body))}, nil
}

const newerHost = "9d092e05d2c2"

var _ = Describe("A client older than its host", func() {
	var warnings []string

	clientOf := func(host cannedHost) *csf.Client {
		client, err := csf.NewClient("http://127.0.0.1:14120", host, csf.WithClientWarnings(func(warning string) { warnings = append(warnings, warning) }))
		Expect(err).NotTo(HaveOccurred())
		return client
	}

	BeforeEach(func() { warnings = nil })

	It("decodes a receipt with a field it does not know, keeps every field it knows, and warns once naming both versions", func() {
		client := clientOf(cannedHost{version: newerHost,
			body: `{"sessions":[{"assignmentId":"a1","executor":"copilot"}],"hostPid":42,"fieldFromTheFuture":true}`})

		response, err := client.ListAgentSessions(context.Background(), &harnessv1.ListAgentSessionsRequest{})

		Expect(err).NotTo(HaveOccurred())
		Expect(response.GetHostPid()).To(Equal(int32(42)))
		Expect(response.GetSessions()).To(HaveLen(1))
		Expect(response.GetSessions()[0].GetAssignmentId()).To(Equal("a1"))
		Expect(warnings).To(HaveLen(1))
		Expect(warnings[0]).To(ContainSubstring("this csf (" + csf.Version() + ")"))
		Expect(warnings[0]).To(ContainSubstring("the host (" + newerHost + ")"))
	})

	It("names the host's version unknown when the host sends none", func() {
		client := clientOf(cannedHost{body: `{"fieldFromTheFuture":1}`})

		_, err := client.ListAgentSessions(context.Background(), &harnessv1.ListAgentSessionsRequest{})

		Expect(err).NotTo(HaveOccurred())
		Expect(warnings).To(ConsistOf(ContainSubstring("the host (" + csf.UnknownVersion + ")")))
	})

	It("does not warn on a receipt of the shape it knows", func() {
		client := clientOf(cannedHost{version: newerHost, body: `{"hostPid":7}`})

		response, err := client.ListAgentSessions(context.Background(), &harnessv1.ListAgentSessionsRequest{})

		Expect(err).NotTo(HaveOccurred())
		Expect(response.GetHostPid()).To(Equal(int32(7)))
		Expect(warnings).To(BeEmpty())
	})

	It("still fails a response that is not a receipt at all", func() {
		client := clientOf(cannedHost{version: newerHost, body: `not json`})

		_, err := client.ListAgentSessions(context.Background(), &harnessv1.ListAgentSessionsRequest{})

		Expect(err).To(HaveOccurred())
		Expect(warnings).To(BeEmpty())
	})

	It("drops warnings when no sink is given", func() {
		client, err := csf.NewClient("http://127.0.0.1:14120", cannedHost{body: `{"fieldFromTheFuture":1}`})
		Expect(err).NotTo(HaveOccurred())

		_, err = client.ListAgentSessions(context.Background(), &harnessv1.ListAgentSessionsRequest{})

		Expect(err).NotTo(HaveOccurred())
	})
})
