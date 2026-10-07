// Copyright 2026 Candace Labs

package jev_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	stdhttp "net/http"
	"slices"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"

	"github.com/candacelabs/csf/io/net/model/jev"
)

// The local Ollama server the Ollama decider talks to, and the decision model
// it reads. A generate answer is one token's ranked alternatives; the answer
// letter's logprobs are the option scores.
const (
	specOllama = "http://127.0.0.1:11434"
	specModel  = "hf.co/example/jevk5-9b-v0.3.3-GGUF:Q4_K_M"
)

// generateAnswer renders a generate answer whose first token ranked the given
// letter logprobs, highest first as the server orders them.
func generateAnswer(letters map[string]float64) string {
	names := make([]string, 0, len(letters))
	for name := range letters {
		names = append(names, name)
	}
	slices.Sort(names)
	top := make([]map[string]any, 0, len(names))
	for _, name := range names {
		top = append(top, map[string]any{"token": name, "logprob": letters[name], "bytes": []int{int(name[0])}})
	}
	payload := map[string]any{
		"response": "A",
		"logprobs": []any{map[string]any{"token": "A", "logprob": letters["A"], "top_logprobs": top}},
	}
	encoded, err := json.Marshal(payload)
	Expect(err).NotTo(HaveOccurred())
	return string(encoded)
}

// generateAnswerWithUsage renders a generate answer as generateAnswer does,
// with the token counts the server reports for the call: the prompt tokens it
// read and the tokens it generated.
func generateAnswerWithUsage(letters map[string]float64, promptTokens, evalTokens int64) string {
	payload := map[string]any{}
	Expect(json.Unmarshal([]byte(generateAnswer(letters)), &payload)).To(Succeed())
	payload["prompt_eval_count"] = promptTokens
	payload["eval_count"] = evalTokens
	encoded, err := json.Marshal(payload)
	Expect(err).NotTo(HaveOccurred())
	return string(encoded)
}

// sentGenerate is a captured /api/generate request.
type sentGenerate struct {
	Model       string `json:"model"`
	Prompt      string `json:"prompt"`
	Raw         bool   `json:"raw"`
	Stream      bool   `json:"stream"`
	Logprobs    bool   `json:"logprobs"`
	TopLogprobs int    `json:"top_logprobs"`
	KeepAlive   string `json:"keep_alive"`
	Options     struct {
		NumPredict int `json:"num_predict"`
	} `json:"options"`
}

// expectGenerates answers the decider's calls, in order, with answers, and
// returns the requests it sent.
func expectGenerates(client *MockIHTTPClient, answers ...string) *[]sentGenerate {
	sent := &[]sentGenerate{}
	client.EXPECT().Do(gomock.Any()).Times(len(answers)).DoAndReturn(func(request *stdhttp.Request) (*stdhttp.Response, error) {
		Expect(request.Method).To(Equal(stdhttp.MethodPost))
		Expect(request.URL.String()).To(Equal(specOllama + "/api/generate"))
		Expect(request.Header.Get("Content-Type")).To(Equal("application/json"))
		content, err := io.ReadAll(request.Body)
		Expect(err).NotTo(HaveOccurred())
		var body sentGenerate
		Expect(json.Unmarshal(content, &body)).To(Succeed())
		index := len(*sent)
		*sent = append(*sent, body)
		return respond(stdhttp.StatusOK, answers[index]), nil
	})
	return sent
}

func newOllamaDecider(client *MockIHTTPClient, options ...jev.OllamaOption) *jev.OllamaDecider {
	decider, err := jev.NewOllamaDecider(client, specModel, append([]jev.OllamaOption{jev.WithOllamaEndpoint(specOllama)}, options...)...)
	Expect(err).NotTo(HaveOccurred())
	return decider
}

var _ = Describe("The JEV Ollama decider", func() {
	var client *MockIHTTPClient

	BeforeEach(func() {
		client = NewMockIHTTPClient(gomock.NewController(GinkgoT()))
	})

	It("reads each question's option letters and returns a distribution in answer order", func() {
		choice := jev.Choice("Supplier response for this scenario.", "issue_warning", "renegotiate", "dual_source", "maintain")
		noul := jev.Noul("Is the supplier reliable?")
		score := jev.Score("How urgent is this ticket?")
		sent := expectGenerates(client,
			// Letters weighted 1, 1/2, 1/3, 1/4 give the exact proportions below.
			generateAnswer(map[string]float64{"A": 0, "B": -0.6931471805599453, "C": -1.0986122886681098, "D": -1.3862943611198906}),
			generateAnswer(map[string]float64{"A": 0, "B": -1.0986122886681098}),
			generateAnswer(map[string]float64{"A": 0, "B": 0, "C": 0, "D": 0, "E": 0, "F": 0}),
		)
		decider := newOllamaDecider(client)
		distributions, err := decider.Decide(context.Background(), supplierState, []jev.Question{choice, noul, score})
		Expect(err).NotTo(HaveOccurred())
		Expect(distributions).To(HaveLen(3))
		Expect(distributions[0].Answers).To(Equal([]string{"issue_warning", "renegotiate", "dual_source", "maintain"}))
		Expect(distributions[0].Probabilities).To(HaveExactElements(
			BeNumerically("~", 0.48, 1e-9),
			BeNumerically("~", 0.24, 1e-9),
			BeNumerically("~", 0.16, 1e-9),
			BeNumerically("~", 0.12, 1e-9),
		))
		Expect(distributions[1].Answers).To(Equal([]string{"false", "true"}))
		Expect(distributions[1].Probabilities[0]).To(BeNumerically("~", 0.75, 1e-9))
		Expect(distributions[2].Probabilities).To(HaveEach(BeNumerically("~", 1.0/6, 1e-9)))

		Expect(*sent).To(HaveLen(3))
		Expect((*sent)[0].Model).To(Equal(specModel))
		Expect((*sent)[0].Raw).To(BeTrue())
		Expect((*sent)[0].Stream).To(BeFalse())
		Expect((*sent)[0].Logprobs).To(BeTrue())
		Expect((*sent)[0].TopLogprobs).To(Equal(20))
		Expect((*sent)[0].Options.NumPredict).To(Equal(1))
		Expect((*sent)[0].KeepAlive).To(Equal("30s"))
		prompt := (*sent)[0].Prompt
		Expect(prompt).To(ContainSubstring(supplierState))
		Expect(prompt).To(ContainSubstring("Supplier response for this scenario."))
		Expect(prompt).To(ContainSubstring("A. issue_warning\n"))
		Expect(prompt).To(ContainSubstring("D. maintain\n"))
		Expect(prompt).To(HaveSuffix("Answer:\n"))
		Expect((*sent)[2].Prompt).To(ContainSubstring("A. 0\n"))
		Expect((*sent)[2].Prompt).To(ContainSubstring("F. 5\n"))
	})

	It("re-tempers the option letters at the decider's temperature", func() {
		expectGenerates(client, generateAnswer(map[string]float64{"A": 0, "B": -0.6931471805599453}))
		decider := newOllamaDecider(client, jev.WithOllamaTemperature(2))
		distributions, err := decider.Decide(context.Background(), supplierState, []jev.Question{jev.Noul("Is it late?")})
		Expect(err).NotTo(HaveOccurred())
		// exp(0) and exp(-ln2/2) = 1 and 0.7071: 1 / 1.7071 = 0.586.
		Expect(distributions[0].Probabilities[0]).To(BeNumerically("~", 0.5858, 0.0001))
		Expect(distributions[0].Probabilities[1]).To(BeNumerically("~", 0.4142, 0.0001))
	})

	It("places an option the model left out below the lowest one it ranked", func() {
		expectGenerates(client, generateAnswer(map[string]float64{"A": 0, "B": -1, "C": -2}))
		decider := newOllamaDecider(client)
		choice := jev.Choice("Pick one.", "a", "b", "c", "d")
		distributions, err := decider.Decide(context.Background(), supplierState, []jev.Question{choice})
		Expect(err).NotTo(HaveOccurred())
		probabilities := distributions[0].Probabilities
		Expect(probabilities[3]).To(BeNumerically(">", 0))
		Expect(probabilities[3]).To(BeNumerically("<", probabilities[2]))
		Expect(probabilities[3]).To(BeNumerically("~", 0.0120, 0.0001))
		Expect(probabilities[0] + probabilities[1] + probabilities[2] + probabilities[3]).To(BeNumerically("~", 1, 1e-9))
	})

	It("reduces a choice wider than 16 options by a knockout and zeroes the eliminated ones", func() {
		options := make([]string, 24)
		for index := range options {
			options[index] = fmt.Sprintf("opt%02d", index)
		}
		wide := jev.Choice("Which option applies?", options...)
		firstGroup := map[string]float64{}
		for index := 0; index < 16; index++ {
			firstGroup[string(rune('A'+index))] = -3
		}
		firstGroup["C"] = 0 // opt02 wins the first group.
		secondGroup := map[string]float64{}
		for index := 0; index < 8; index++ {
			secondGroup[string(rune('A'+index))] = -3
		}
		secondGroup["E"] = 0 // opt20 wins the second group.
		sent := expectGenerates(client,
			generateAnswer(firstGroup),
			generateAnswer(secondGroup),
			generateAnswer(map[string]float64{"A": 0, "B": -0.6931471805599453}), // opt02 and opt20: 0.8 / 0.2 at T=0.5.
		)
		decider := newOllamaDecider(client, jev.WithOllamaTemperature(1), jev.WithOllamaKnockoutTemperature(0.5))
		distributions, err := decider.Decide(context.Background(), supplierState, []jev.Question{wide})
		Expect(err).NotTo(HaveOccurred())
		Expect(*sent).To(HaveLen(3))
		Expect((*sent)[0].Prompt).To(ContainSubstring("A. opt00\n"))
		Expect((*sent)[0].Prompt).To(ContainSubstring("P. opt15\n"))
		Expect((*sent)[0].Prompt).NotTo(ContainSubstring("opt16"))
		Expect((*sent)[1].Prompt).To(ContainSubstring("A. opt16\n"))
		Expect((*sent)[1].Prompt).To(ContainSubstring("H. opt23\n"))
		Expect((*sent)[2].Prompt).To(ContainSubstring("A. opt02\n"))
		Expect((*sent)[2].Prompt).To(ContainSubstring("B. opt20\n"))
		probabilities := distributions[0].Probabilities
		Expect(probabilities).To(HaveLen(24))
		for index, probability := range probabilities {
			if index == 2 || index == 20 {
				continue
			}
			Expect(probability).To(BeZero(), "option %d", index)
		}
		Expect(probabilities[2]).To(BeNumerically("~", 0.8, 1e-9))
		Expect(probabilities[20]).To(BeNumerically("~", 0.2, 1e-9))
	})

	It("refuses an answer that carries no option letters", func() {
		expectGenerates(client, `{"response":"A"}`)
		decider := newOllamaDecider(client)
		_, err := decider.Decide(context.Background(), supplierState, []jev.Question{jev.Noul("Is it late?")})
		Expect(err).To(MatchError(jev.ErrLetterShape))
		expectGenerates(client, `{"logprobs":[{"token":"Answer","top_logprobs":[{"token":"Answer","logprob":-1}]}]}`)
		_, err = decider.Decide(context.Background(), supplierState, []jev.Question{jev.Noul("Is it late?")})
		Expect(err).To(MatchError(ContainSubstring("none of the 2 option letters A..B were ranked")))
	})

	It("reports an HTTP refusal with the status and text", func() {
		client.EXPECT().Do(gomock.Any()).Return(respond(stdhttp.StatusNotFound, `{"error":"model not found"}`), nil)
		decider := newOllamaDecider(client)
		_, err := decider.Decide(context.Background(), supplierState, []jev.Question{jev.Noul("Is it late?")})
		Expect(err).To(MatchError(ContainSubstring("HTTP 404")))
		Expect(err).To(MatchError(ContainSubstring("model not found")))
	})

	It("reports a transport failure", func() {
		client.EXPECT().Do(gomock.Any()).Return(nil, errors.New("dial tcp: connection refused"))
		decider := newOllamaDecider(client)
		_, err := decider.Decide(context.Background(), supplierState, []jev.Question{jev.Noul("Is it late?")})
		Expect(err).To(MatchError(ContainSubstring("connection refused")))
	})

	It("sends nothing for an empty state, no questions or an invalid question", func() {
		decider := newOllamaDecider(client)
		_, err := decider.Decide(context.Background(), " \n", []jev.Question{jev.Noul("Is it late?")})
		Expect(err).To(MatchError(jev.ErrNoState))
		_, err = decider.Decide(context.Background(), supplierState, nil)
		Expect(err).To(MatchError(jev.ErrNoQuestions))
		_, err = decider.Decide(context.Background(), supplierState, []jev.Question{{Kind: "rank", Question: "Rank these."}})
		Expect(err).To(MatchError(jev.ErrInvalidQuestion))
		tooMany := make([]string, jev.MaxKnockoutOptions+1)
		for index := range tooMany {
			tooMany[index] = fmt.Sprintf("opt%d", index)
		}
		_, err = decider.Decide(context.Background(), supplierState, []jev.Question{jev.Choice("Which?", tooMany...)})
		Expect(err).To(MatchError(jev.ErrInvalidQuestion))
	})

	It("reports the tokens the server counted, summed over the questions", func() {
		expectGenerates(client,
			generateAnswerWithUsage(map[string]float64{"A": 0, "B": -0.6931471805599453}, 512, 3),
			generateAnswerWithUsage(map[string]float64{"A": 0, "B": 0, "C": 0, "D": 0, "E": 0, "F": 0}, 640, 1),
		)
		decider := newOllamaDecider(client)
		_, usage, err := decider.DecideWithUsage(context.Background(), supplierState, []jev.Question{jev.Noul("Is it late?"), jev.Score("How urgent?")})
		Expect(err).NotTo(HaveOccurred())
		Expect(usage.PromptTokens).To(Equal(int64(1152)))
		Expect(usage.EvalTokens).To(Equal(int64(4)))
		Expect(usage.Total()).To(Equal(int64(1156)))
	})
})

var _ = Describe("Building the JEV Ollama decider", func() {
	It("requires the http capability and a model name", func() {
		_, err := jev.NewOllamaDecider(nil, specModel)
		Expect(err).To(MatchError(jev.ErrNoClient))
		_, err = jev.NewOllamaDecider(NewMockIHTTPClient(gomock.NewController(GinkgoT())), "  ")
		Expect(err).To(MatchError(jev.ErrNoModel))
	})

	It("rejects a nil or unusable option", func() {
		client := NewMockIHTTPClient(gomock.NewController(GinkgoT()))
		for _, options := range [][]jev.OllamaOption{
			{nil},
			{jev.WithOllamaEndpoint("  ")},
			{jev.WithOllamaTemperature(0)},
			{jev.WithOllamaKnockoutTemperature(-1)},
			{jev.WithOllamaKeepAlive(-1)},
		} {
			_, err := jev.NewOllamaDecider(client, specModel, options...)
			Expect(err).To(MatchError(jev.ErrInvalidOption))
		}
	})
})
