package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Checkpoint links", func() {
	var content []byte
	var handoff handoffLinks
	BeforeEach(func() {
		// Synthetic consumer handoff; no operator runtime evidence is shipped.
		content = []byte(`{"schema_version":1,"repository":{"origin":"https://github.com/example/csf.git"},"export":{"source_revision":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","review_url":"https://example.invalid/releases/review"},"live":{"workbench":{"url":"https://example.invalid/ui/","kanban_url":"https://example.invalid/ui/#/kanban"}}}`)
		Expect(json.Unmarshal(content, &handoff)).To(Succeed())
	})

	It("prints all six links from the retained handoff and pins source links", func() {
		var output bytes.Buffer
		Expect(renderHandoffLinks(content, &output)).To(Succeed())
		Expect(strings.Count(output.String(), "](<")).To(Equal(6))
		Expect(output.String()).To(ContainSubstring(handoff.Export.ReviewURL))
		Expect(output.String()).To(ContainSubstring(handoff.Live.Workbench.URL))
		Expect(output.String()).To(ContainSubstring(handoff.Live.Workbench.KanbanURL))
		Expect(strings.Count(output.String(), handoff.Export.SourceRevision)).To(Equal(3))
		Expect(output.String()).To(ContainSubstring("README.md#mechanical-extension-path"))
	})

	It("uses the same rendering for an explicit local file and HTTP handoff", func() {
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			_, err := writer.Write(content)
			Expect(err).NotTo(HaveOccurred())
		}))
		DeferCleanup(server.Close)
		var local, remote bytes.Buffer
		file := filepath.Join(GinkgoT().TempDir(), "handoff.json")
		Expect(os.WriteFile(file, content, 0600)).To(Succeed())
		Expect(links([]string{"--handoff", file}, &local)).To(Succeed())
		Expect(links([]string{"--handoff", server.URL}, &remote)).To(Succeed())
		Expect(remote.String()).To(Equal(local.String()))
	})

	It("returns an error without a partial table when a required link is missing", func() {
		handoff.Live.Workbench.KanbanURL = ""
		input, err := json.Marshal(handoff)
		Expect(err).NotTo(HaveOccurred())
		var output bytes.Buffer
		Expect(renderHandoffLinks(input, &output)).NotTo(Succeed())
		Expect(output.Len()).To(BeZero())
	})

	It("rejects an abbreviated source revision", func() {
		handoff.Export.SourceRevision = "53d169cb5"
		input, err := json.Marshal(handoff)
		Expect(err).NotTo(HaveOccurred())
		Expect(renderHandoffLinks(input, &bytes.Buffer{})).To(MatchError(ContainSubstring("full source commit")))
	})

	It("does not turn a missing HTTP document into a link table", func() {
		server := httptest.NewServer(http.NotFoundHandler())
		DeferCleanup(server.Close)
		Expect(links([]string{"--handoff", server.URL}, &bytes.Buffer{})).To(MatchError(ContainSubstring("HTTP 404")))
	})

	It("enforces the handoff size limit", func() {
		_, err := readBoundedHandoff(strings.NewReader(strings.Repeat(" ", handoffLimit+1)))
		Expect(err).To(MatchError(ContainSubstring("handoff exceeds")))
	})
})
