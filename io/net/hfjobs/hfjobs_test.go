// Copyright 2026 Candace Labs

package hfjobs_test

import (
	"context"
	"encoding/json"
	"io"
	stdhttp "net/http"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"

	"github.com/candacelabs/csf/io/net/hfjobs"
)

// Integration specs: the client through its exported API, over a gomock http
// capability. hardwareBody is what /api/jobs/hardware returned on 2026-10-05
// (its first entry); jobBody is huggingface_hub's documented JobInfo shape.
const (
	specEndpoint  = "https://hub.example"
	specNamespace = "operator"
	specToken     = "hf_spec"
	specJobID     = "687fb701029421ae5549d998"

	hardwareBody = `[{"name":"cpu-basic","prettyName":"CPU Basic","cpu":"2 vCPU","ram":"16 GB","ephemeralStorage":"50 GB","accelerator":null,"unitCostMicroUSD":167,"unitCostUSD":0.000167,"unitLabel":"minute"}]`
	jobBody      = `{"id":"687fb701029421ae5549d998","createdAt":"2025-07-22T16:06:25.079Z","startedAt":"2025-07-22T16:06:31.079Z","dockerImage":"python:3.12","command":["python","-c","print('hello')"],"arguments":[],"environment":{},"secrets":{},"flavor":"cpu-basic","labels":{"csf":"burst"},"status":{"stage":"RUNNING","message":null},"owner":{"id":"5e9ecfc04957053f60648a3e","name":"operator","type":"user"}}`
)

func respond(status int, body string, header stdhttp.Header) *stdhttp.Response {
	if header == nil {
		header = stdhttp.Header{}
	}
	return &stdhttp.Response{StatusCode: status, Header: header, Body: io.NopCloser(strings.NewReader(body))}
}

var _ = Describe("The Hugging Face Jobs client", func() {
	var client *MockIHTTPClient
	var jobs *hfjobs.HFJobsClient
	ctx := context.Background()

	BeforeEach(func() {
		client = NewMockIHTTPClient(gomock.NewController(GinkgoT()))
		var err error
		jobs, err = hfjobs.NewHFJobsClient(client, specNamespace, specToken, hfjobs.WithEndpoint(specEndpoint+"/"))
		Expect(err).NotTo(HaveOccurred())
	})

	It("starts a job with the spec as the API takes it, secrets included, and the token as a bearer", func() {
		client.EXPECT().Do(gomock.Any()).DoAndReturn(func(request *stdhttp.Request) (*stdhttp.Response, error) {
			Expect(request.Method).To(Equal(stdhttp.MethodPost))
			Expect(request.URL.String()).To(Equal(specEndpoint + "/api/jobs/" + specNamespace))
			Expect(request.Header.Get("Authorization")).To(Equal("Bearer " + specToken))
			content, err := io.ReadAll(request.Body)
			Expect(err).NotTo(HaveOccurred())
			var sent map[string]any
			Expect(json.Unmarshal(content, &sent)).To(Succeed())
			Expect(sent).To(HaveKeyWithValue("dockerImage", "csf-session"))
			Expect(sent).To(HaveKeyWithValue("flavor", "cpu-xl"))
			Expect(sent).To(HaveKeyWithValue("timeoutSeconds", BeNumerically("==", 600)))
			Expect(sent).To(HaveKeyWithValue("secrets", map[string]any{"GH_TOKEN": "secret"}))
			Expect(sent).To(HaveKeyWithValue("arguments", []any{}))
			Expect(sent).To(HaveKeyWithValue("environment", map[string]any{}))
			return respond(stdhttp.StatusOK, jobBody, nil), nil
		})
		job, err := jobs.Run(ctx, hfjobs.Spec{DockerImage: "csf-session", Command: []string{"csf"}, Flavor: "cpu-xl", TimeoutSeconds: 600, Secrets: map[string]string{"GH_TOKEN": "secret"}})
		Expect(err).NotTo(HaveOccurred())
		Expect(job.ID).To(Equal(specJobID))
		Expect(job.Status.Stage).To(Equal(hfjobs.StageRunning))
		Expect(job.StartedAt).NotTo(BeNil())
		Expect(job.Labels).To(HaveKeyWithValue("csf", "burst"))
	})

	It("refuses a spec without an image, a command or a flavor before calling", func() {
		_, err := jobs.Run(ctx, hfjobs.Spec{DockerImage: "csf-session", Flavor: "cpu-xl"})
		Expect(err).To(MatchError(hfjobs.ErrInvalidSpec))
	})

	It("lists every page the API links", func() {
		second := specEndpoint + "/api/jobs/" + specNamespace + "?cursor=2"
		gomock.InOrder(
			client.EXPECT().Do(gomock.Any()).DoAndReturn(func(request *stdhttp.Request) (*stdhttp.Response, error) {
				Expect(request.URL.String()).To(Equal(specEndpoint + "/api/jobs/" + specNamespace))
				return respond(stdhttp.StatusOK, "["+jobBody+"]", stdhttp.Header{"Link": {"<" + second + `>; rel="next"`}}), nil
			}),
			client.EXPECT().Do(gomock.Any()).DoAndReturn(func(request *stdhttp.Request) (*stdhttp.Response, error) {
				Expect(request.URL.String()).To(Equal(second))
				return respond(stdhttp.StatusOK, "[]", nil), nil
			}),
		)
		listed, err := jobs.List(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(listed).To(HaveLen(1))
	})

	It("cancels one job by its own route", func() {
		client.EXPECT().Do(gomock.Any()).DoAndReturn(func(request *stdhttp.Request) (*stdhttp.Response, error) {
			Expect(request.Method).To(Equal(stdhttp.MethodPost))
			Expect(request.URL.Path).To(Equal("/api/jobs/" + specNamespace + "/" + specJobID + "/cancel"))
			return respond(stdhttp.StatusOK, "", nil), nil
		})
		Expect(jobs.Cancel(ctx, specJobID)).To(Succeed())
	})

	It("refuses a job identifier that would leave the namespace's routes", func() {
		Expect(jobs.Cancel(ctx, "../other")).To(MatchError(hfjobs.ErrInvalidJobID))
		_, err := jobs.Inspect(ctx, "a/b")
		Expect(err).To(MatchError(hfjobs.ErrInvalidJobID))
	})

	It("reads each flavor's price", func() {
		client.EXPECT().Do(gomock.Any()).Return(respond(stdhttp.StatusOK, hardwareBody, nil), nil)
		hardware, err := jobs.Hardware(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(hardware).To(Equal([]hfjobs.Hardware{{Name: "cpu-basic", PrettyName: "CPU Basic", CPU: "2 vCPU", RAM: "16 GB", EphemeralStorage: "50 GB", UnitCostMicroUSD: 167, UnitLabel: "minute"}}))
	})

	It("reports a refusal with the API's own message", func() {
		client.EXPECT().Do(gomock.Any()).Return(respond(stdhttp.StatusForbidden, `{"error":"Pro subscription required"}`, nil), nil)
		_, err := jobs.Inspect(ctx, specJobID)
		Expect(err).To(MatchError(&hfjobs.StatusError{Status: stdhttp.StatusForbidden, Message: "Pro subscription required"}))
	})

	It("names the job's own page", func() {
		Expect(jobs.JobURL(specJobID)).To(Equal(specEndpoint + "/jobs/" + specNamespace + "/" + specJobID))
	})

	It("refuses to build without its capability, token or namespace", func() {
		_, err := hfjobs.NewHFJobsClient(nil, specNamespace, specToken)
		Expect(err).To(MatchError(hfjobs.ErrNoClient))
		_, err = hfjobs.NewHFJobsClient(client, specNamespace, "")
		Expect(err).To(MatchError(hfjobs.ErrNoToken))
		_, err = hfjobs.NewHFJobsClient(client, "a/b", specToken)
		Expect(err).To(MatchError(hfjobs.ErrNoNamespace))
	})

	It("knows which stages are final", func() {
		Expect(hfjobs.StageRunning.Terminal()).To(BeFalse())
		Expect(hfjobs.StageScheduling.Terminal()).To(BeFalse())
		Expect(hfjobs.StageCanceled.Terminal()).To(BeTrue())
		Expect(hfjobs.StageCompleted.Terminal()).To(BeTrue())
	})
})
