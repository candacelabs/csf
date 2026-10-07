// Copyright 2026 Candace Labs

package jev_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	stdhttp "net/http"
	"os"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"

	"github.com/candacelabs/csf/io/net/model/jev"
)

// The testdata streams are what the authors' Space returned on 2026-10-06:
// space_complete.sse for the supplier state below with the choice and noul
// questions, space_error.sse for a call with no questions.
const (
	specEndpoint  = "https://jev.example.hf.space"
	supplierState = "SKU AX-330 stock at 8% of safety level; supplier late twice this quarter."
	queuedBody    = `{"event_id":"8e045876242d458db92623c398621338"}`
)

var supplierQuestions = []jev.Question{
	jev.Choice("Supplier response for this scenario.", "issue_warning", "renegotiate", "dual_source", "maintain"),
	jev.Noul("Is the supplier reliable?"),
}

func respond(status int, body string) *stdhttp.Response {
	return &stdhttp.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body))}
}

func recorded(name string) string {
	content, err := os.ReadFile("testdata/" + name)
	Expect(err).NotTo(HaveOccurred())
	return string(content)
}

// expectCall answers one submit with queued and the stream fetch with stream,
// and returns the inputs the submit carried.
func expectCall(client *MockIHTTPClient, queued string, stream string) *[]string {
	sent := &[]string{}
	submit := client.EXPECT().Do(gomock.Any()).DoAndReturn(func(request *stdhttp.Request) (*stdhttp.Response, error) {
		Expect(request.Method).To(Equal(stdhttp.MethodPost))
		Expect(request.URL.String()).To(Equal(specEndpoint + "/gradio_api/call/decide"))
		var body struct {
			Data []string `json:"data"`
		}
		content, err := io.ReadAll(request.Body)
		Expect(err).NotTo(HaveOccurred())
		Expect(json.Unmarshal(content, &body)).To(Succeed())
		*sent = body.Data
		return respond(stdhttp.StatusOK, queued), nil
	})
	client.EXPECT().Do(gomock.Any()).After(submit).DoAndReturn(func(request *stdhttp.Request) (*stdhttp.Response, error) {
		Expect(request.Method).To(Equal(stdhttp.MethodGet))
		Expect(request.URL.String()).To(Equal(specEndpoint + "/gradio_api/call/decide/8e045876242d458db92623c398621338"))
		return respond(stdhttp.StatusOK, stream), nil
	})
	return sent
}

var _ = Describe("The JEV Space decider", func() {
	var client *MockIHTTPClient
	var decider *jev.SpaceDecider

	BeforeEach(func() {
		client = NewMockIHTTPClient(gomock.NewController(GinkgoT()))
		var err error
		decider, err = jev.NewSpaceDecider(client, specEndpoint+"/")
		Expect(err).NotTo(HaveOccurred())
	})

	It("sends the state and one row per question slot, and returns each distribution in answer order", func() {
		sent := expectCall(client, queuedBody, recorded("space_complete.sse"))
		distributions, err := decider.Decide(context.Background(), supplierState, supplierQuestions)
		Expect(err).NotTo(HaveOccurred())
		Expect(*sent).To(Equal([]string{
			supplierState,
			"choice", "Supplier response for this scenario.", "issue_warning, renegotiate, dual_source, maintain",
			"noul", "Is the supplier reliable?", "",
			"", "", "",
			"", "", "",
		}))
		Expect(distributions).To(HaveLen(2))
		Expect(distributions[0].Answers).To(Equal([]string{"issue_warning", "renegotiate", "dual_source", "maintain"}))
		Expect(distributions[0].Probabilities).To(Equal([]float64{0.3314894139766693, 0.13940627872943878, 0.5281404256820679, 0.0009639220661483705}))
		Expect(distributions[1].Answers).To(Equal([]string{"false", "true"}))
		Expect(distributions[1].Probabilities).To(Equal([]float64{0.9432799220085144, 0.05672008544206619}))
		top, probability := distributions[0].Top()
		Expect(top).To(Equal("dual_source"))
		Expect(probability).To(BeNumerically("~", 0.528, 0.001))
	})

	It("sends the Hugging Face token when it has one", func() {
		tokened, err := jev.NewSpaceDecider(client, specEndpoint, jev.WithSpaceToken(" hf_example \n"))
		Expect(err).NotTo(HaveOccurred())
		client.EXPECT().Do(gomock.Any()).Times(2).DoAndReturn(func(request *stdhttp.Request) (*stdhttp.Response, error) {
			Expect(request.Header.Get("Authorization")).To(Equal("Bearer hf_example"))
			if request.Method == stdhttp.MethodPost {
				return respond(stdhttp.StatusOK, queuedBody), nil
			}
			return respond(stdhttp.StatusOK, recorded("space_complete.sse")), nil
		})
		_, err = tokened.Decide(context.Background(), supplierState, supplierQuestions)
		Expect(err).NotTo(HaveOccurred())
	})

	It("splits more questions than one call holds into several calls, keeping question order", func() {
		questions := append(append([]jev.Question{}, supplierQuestions...), supplierQuestions...)
		questions = append(questions, supplierQuestions...)
		// The first call carries four questions, so its answer is the recorded
		// one with two more answers placed in front.
		expectCall(client, queuedBody, strings.Replace(recorded("space_complete.sse"),
			`\"answers\": [`, `\"answers\": [{\"issue_warning\": 0.1, \"renegotiate\": 0.2, \"dual_source\": 0.3, \"maintain\": 0.4}, {\"false\": 0.5, \"true\": 0.5},`, 1))
		second := expectCall(client, queuedBody, recorded("space_complete.sse"))
		distributions, err := decider.Decide(context.Background(), supplierState, questions)
		Expect(err).NotTo(HaveOccurred())
		Expect(distributions).To(HaveLen(6))
		Expect((*second)[1:4]).To(Equal([]string{"choice", "Supplier response for this scenario.", "issue_warning, renegotiate, dual_source, maintain"}))
		Expect(distributions[0].Probabilities).To(Equal([]float64{0.1, 0.2, 0.3, 0.4}))
		Expect(distributions[4].Probabilities[2]).To(Equal(0.5281404256820679))
	})

	It("reports the Space's refusal with its message", func() {
		expectCall(client, queuedBody, recorded("space_error.sse"))
		_, err := decider.Decide(context.Background(), supplierState, supplierQuestions)
		Expect(err).To(MatchError(jev.ErrSpaceRefused))
		Expect(err).To(MatchError(ContainSubstring("Add at least one question")))
	})

	It("refuses answers that do not cover the questions", func() {
		expectCall(client, queuedBody, recorded("space_complete.sse"))
		_, err := decider.Decide(context.Background(), supplierState, supplierQuestions[:1])
		Expect(err).To(MatchError(jev.ErrAnswerShape))
	})

	It("refuses an answer missing one of the question's options", func() {
		expectCall(client, queuedBody, recorded("space_complete.sse"))
		renamed := []jev.Question{jev.Choice("Supplier response for this scenario.", "issue_warning", "renegotiate", "dual_source", "hold"), supplierQuestions[1]}
		_, err := decider.Decide(context.Background(), supplierState, renamed)
		Expect(err).To(MatchError(ContainSubstring(`no probability for "hold"`)))
	})

	It("refuses an event identifier that is not one before building a URL from it", func() {
		client.EXPECT().Do(gomock.Any()).Return(respond(stdhttp.StatusOK, `{"event_id":"../../admin"}`), nil)
		_, err := decider.Decide(context.Background(), supplierState, supplierQuestions)
		Expect(err).To(MatchError(ContainSubstring("did not queue the call")))
	})

	It("reports a stream that ends without a result", func() {
		expectCall(client, queuedBody, "event: heartbeat\ndata: null\n\n")
		_, err := decider.Decide(context.Background(), supplierState, supplierQuestions)
		Expect(err).To(MatchError(ContainSubstring("ended without a result")))
	})

	It("reports an HTTP refusal with the status and text", func() {
		client.EXPECT().Do(gomock.Any()).Return(respond(stdhttp.StatusTooManyRequests, "ZeroGPU quota exceeded"), nil)
		_, err := decider.Decide(context.Background(), supplierState, supplierQuestions)
		Expect(err).To(MatchError(ContainSubstring("HTTP 429: ZeroGPU quota exceeded")))
	})

	It("reports a transport failure", func() {
		client.EXPECT().Do(gomock.Any()).Return(nil, errors.New("dial tcp: connection refused"))
		_, err := decider.Decide(context.Background(), supplierState, supplierQuestions)
		Expect(err).To(MatchError(ContainSubstring("connection refused")))
	})

	It("sends nothing for an empty state, no questions or an invalid question", func() {
		_, err := decider.Decide(context.Background(), " \n", supplierQuestions)
		Expect(err).To(MatchError(jev.ErrNoState))
		_, err = decider.Decide(context.Background(), supplierState, nil)
		Expect(err).To(MatchError(jev.ErrNoQuestions))
		for _, question := range []jev.Question{
			jev.Noul(" "),
			{Kind: "rank", Question: "Rank these."},
			jev.Choice("Pick one.", "only"),
			jev.Choice("Pick one.", "a", "a"),
			jev.Choice("Pick one.", "a", " b"),
			jev.Choice("Pick one.", "a", "b, c"),
		} {
			_, err = decider.Decide(context.Background(), supplierState, []jev.Question{question})
			Expect(err).To(MatchError(jev.ErrInvalidQuestion))
		}
	})
})

var _ = Describe("A JEV distribution", func() {
	It("names the fixed answers of noul and score questions", func() {
		Expect(jev.Noul("It is late.").Answers()).To(Equal([]string{"false", "true"}))
		Expect(jev.Score("How urgent?").Answers()).To(Equal([]string{"0", "1", "2", "3", "4", "5"}))
	})

	It("gives the expected level of a score question and its top level", func() {
		distribution := &jev.Distribution{
			Question:      jev.Score("How urgent is this ticket?"),
			Answers:       jev.Score("How urgent is this ticket?").Answers(),
			Probabilities: []float64{0.0028352702502161264, 0.01707780547440052, 0.0933370515704155, 0.4675634503364563, 0.41072896122932434, 0.008457389660179615},
		}
		Expect(distribution.Expected()).To(BeNumerically("~", 3.2916, 0.0001))
		top, _ := distribution.Top()
		Expect(top).To(Equal("3"))
	})
})

var _ = Describe("Building the JEV Space decider", func() {
	It("requires the http capability and an endpoint", func() {
		_, err := jev.NewSpaceDecider(nil, specEndpoint)
		Expect(err).To(MatchError(jev.ErrNoClient))
		_, err = jev.NewSpaceDecider(NewMockIHTTPClient(gomock.NewController(GinkgoT())), " ")
		Expect(err).To(MatchError(jev.ErrNoEndpoint))
	})
})
