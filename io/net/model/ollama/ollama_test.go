// Copyright 2026 Candace Labs

package ollama_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	stdhttp "net/http"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"

	"github.com/candacelabs/csf/io/net/model"
	"github.com/candacelabs/csf/io/net/model/ollama"
)

// Integration specs: the brain through its exported API, over a gomock http
// capability. The answer bodies are what the server on this host returned
// on 2026-10-04 for qwen3:8b, copied rather than invented.
const (
	specEndpoint = "http://ollama.example:11434"
	specModel    = "qwen3:8b"

	answerBody = `{"model":"qwen3:8b","created_at":"2026-10-04T03:44:10.379934267Z","message":{"role":"assistant","content":"{\"ok\": true}"},"done":true,"done_reason":"stop","total_duration":4842640266,"load_duration":4662908774,"prompt_eval_count":26,"prompt_eval_duration":49832028,"eval_count":6,"eval_duration":49262714}`
	loadedBody = `{"models":[{"name":"qwen3:8b","model":"qwen3:8b","size":6926614528,"digest":"500a1f067a9f","size_vram":6926614528,"expires_at":"2026-10-04T03:47:10.4Z"}]}`
	notFound   = `{"error":"model 'qwen3:8b' not found"}`

	// embedBody is the server's answer of 2026-10-05 for qwen3-embedding:0.6b
	// with each 1024-dimension vector cut to its first three values.
	embedModel = "qwen3-embedding:0.6b"
	embedBody  = `{"model":"qwen3-embedding:0.6b","embeddings":[[0.028935967,-0.04255417,-0.0115747275],[0.03574131,-0.05316894,-0.005822348]],"total_duration":9952294248,"load_duration":8034754942,"prompt_eval_count":10}`
)

func respond(status int, body string) *stdhttp.Response {
	return &stdhttp.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body))}
}

// decodeRequest reads the chat request the brain sent.
func decodeRequest(request *stdhttp.Request) map[string]any {
	GinkgoHelper()
	content, err := io.ReadAll(request.Body)
	Expect(err).NotTo(HaveOccurred())
	var decoded map[string]any
	Expect(json.Unmarshal(content, &decoded)).To(Succeed())
	return decoded
}

var _ = Describe("The Ollama brain", func() {
	var client *MockIHTTPClient
	var brain *ollama.OllamaBrain

	BeforeEach(func() {
		client = NewMockIHTTPClient(gomock.NewController(GinkgoT()))
		var err error
		brain, err = ollama.NewOllamaBrain(client, specModel, ollama.WithEndpoint(specEndpoint+"/"), ollama.WithContextWindow(8192))
		Expect(err).NotTo(HaveOccurred())
	})

	It("satisfies the brain contract", func() {
		var _ model.IBrain[*ollama.Prompt, ollama.Answer] = brain
		Expect(brain.Model()).To(Equal(specModel))
	})

	It("posts the prompt to the chat API and proposes the answer with its measurements", func() {
		var sent *stdhttp.Request
		client.EXPECT().Do(gomock.Any()).DoAndReturn(func(request *stdhttp.Request) (*stdhttp.Response, error) {
			sent = request
			return respond(stdhttp.StatusOK, answerBody), nil
		})
		proposal, err := brain.Propose(context.Background(), &ollama.Prompt{
			System: "answer JSON", User: "is it ok?", Schema: json.RawMessage(`{"type":"object"}`), KeepAlive: 3 * time.Minute,
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(sent.Method).To(Equal(stdhttp.MethodPost))
		Expect(sent.URL.String()).To(Equal(specEndpoint + "/api/chat"))
		Expect(sent.Header.Get("Content-Type")).To(Equal("application/json"))
		request := decodeRequest(sent)
		Expect(request).To(HaveKeyWithValue("model", specModel))
		Expect(request).To(HaveKeyWithValue("stream", false))
		Expect(request).To(HaveKeyWithValue("keep_alive", "3m0s"))
		Expect(request).To(HaveKeyWithValue("format", map[string]any{"type": "object"}))
		Expect(request["messages"]).To(HaveLen(2))

		Expect(proposal.Provider).To(Equal(ollama.ProviderName))
		answer, err := proposal.Only()
		Expect(err).NotTo(HaveOccurred())
		Expect(answer.Content).To(Equal(`{"ok": true}`))
		Expect(answer.Usage).To(Equal(ollama.Usage{
			Load: 4662908774, PromptTokens: 26, PromptEval: 49832028, Tokens: 6, Eval: 49262714, Total: 4842640266,
		}))
	})

	It("reports the server's refusal with its status and message", func() {
		client.EXPECT().Do(gomock.Any()).Return(respond(stdhttp.StatusNotFound, notFound), nil)
		_, err := brain.Propose(context.Background(), &ollama.Prompt{User: "hello"})
		var refused *ollama.StatusError
		Expect(errors.As(err, &refused)).To(BeTrue(), err.Error())
		Expect(refused.Status).To(Equal(stdhttp.StatusNotFound))
		Expect(refused.Message).To(Equal("model 'qwen3:8b' not found"))
	})

	It("reports a transport failure without an answer", func() {
		client.EXPECT().Do(gomock.Any()).Return(nil, errors.New("dial tcp: connection refused"))
		_, err := brain.Propose(context.Background(), &ollama.Prompt{User: "hello"})
		Expect(err).To(MatchError(ContainSubstring("connection refused")))
	})

	It("refuses an answer the model did not finish", func() {
		client.EXPECT().Do(gomock.Any()).Return(respond(stdhttp.StatusOK, `{"message":{"content":"{"},"done":false}`), nil)
		_, err := brain.Propose(context.Background(), &ollama.Prompt{User: "hello"})
		Expect(err).To(MatchError(ollama.ErrIncompleteAnswer))
	})

	It("refuses a prompt with no user message", func() {
		_, err := brain.Propose(context.Background(), nil)
		Expect(err).To(MatchError(ollama.ErrNoPrompt))
		_, err = brain.Propose(context.Background(), &ollama.Prompt{System: "only a system message"})
		Expect(err).To(MatchError(ollama.ErrNoPrompt))
	})

	It("lists the loaded models with their GPU memory and expiry", func() {
		client.EXPECT().Do(gomock.Any()).DoAndReturn(func(request *stdhttp.Request) (*stdhttp.Response, error) {
			Expect(request.Method).To(Equal(stdhttp.MethodGet))
			Expect(request.URL.String()).To(Equal(specEndpoint + "/api/ps"))
			return respond(stdhttp.StatusOK, loadedBody), nil
		})
		loaded, err := brain.Loaded(context.Background())
		Expect(err).NotTo(HaveOccurred())
		Expect(loaded).To(Equal([]ollama.LoadedModel{{
			Name: specModel, VRAMBytes: 6926614528, ExpiresAt: time.Date(2026, 10, 4, 3, 47, 10, 400000000, time.UTC),
		}}))
	})
})

var _ = Describe("Embedding through the Ollama brain", func() {
	var client *MockIHTTPClient
	var brain *ollama.OllamaBrain

	BeforeEach(func() {
		client = NewMockIHTTPClient(gomock.NewController(GinkgoT()))
		var err error
		brain, err = ollama.NewOllamaBrain(client, embedModel, ollama.WithEndpoint(specEndpoint))
		Expect(err).NotTo(HaveOccurred())
	})

	It("posts the inputs to the embed API and returns one vector per input, in order", func() {
		var sent map[string]any
		client.EXPECT().Do(gomock.Any()).DoAndReturn(func(request *stdhttp.Request) (*stdhttp.Response, error) {
			Expect(request.URL.String()).To(Equal(specEndpoint + "/api/embed"))
			sent = decodeRequest(request)
			return respond(stdhttp.StatusOK, embedBody), nil
		})
		vectors, err := brain.Embed(context.Background(), []string{"csf upgrade verb", "func upgrade() error"}, time.Minute)
		Expect(err).NotTo(HaveOccurred())
		Expect(sent).To(HaveKeyWithValue("model", embedModel))
		Expect(sent).To(HaveKeyWithValue("input", []any{"csf upgrade verb", "func upgrade() error"}))
		Expect(sent).To(HaveKeyWithValue("truncate", true))
		Expect(sent).To(HaveKeyWithValue("keep_alive", "1m0s"))
		Expect(vectors).To(Equal([][]float32{{0.028935967, -0.04255417, -0.0115747275}, {0.03574131, -0.05316894, -0.005822348}}))
	})

	It("sends nothing for no inputs", func() {
		vectors, err := brain.Embed(context.Background(), nil, 0)
		Expect(err).NotTo(HaveOccurred())
		Expect(vectors).To(BeEmpty())
	})

	It("refuses an answer whose vectors do not match its inputs", func() {
		client.EXPECT().Do(gomock.Any()).Return(respond(stdhttp.StatusOK, embedBody), nil)
		_, err := brain.Embed(context.Background(), []string{"only one"}, 0)
		Expect(err).To(MatchError(ollama.ErrEmbeddingCount))
	})
})

var _ = Describe("Building the Ollama brain", func() {
	It("requires the http capability and a model", func() {
		client := NewMockIHTTPClient(gomock.NewController(GinkgoT()))
		_, err := ollama.NewOllamaBrain(nil, specModel)
		Expect(err).To(MatchError(ollama.ErrNoClient))
		_, err = ollama.NewOllamaBrain(client, "")
		Expect(err).To(MatchError(ollama.ErrNoModel))
	})

	It("refuses an option it cannot use", func() {
		client := NewMockIHTTPClient(gomock.NewController(GinkgoT()))
		for _, option := range []ollama.OllamaBrainOption{nil, ollama.WithEndpoint(""), ollama.WithContextWindow(0), ollama.WithTemperature(-1)} {
			_, err := ollama.NewOllamaBrain(client, specModel, option)
			Expect(err).To(MatchError(ollama.ErrInvalidOption))
		}
	})
})
