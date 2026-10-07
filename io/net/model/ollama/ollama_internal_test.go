// Copyright 2026 Candace Labs

package ollama

import (
	"encoding/json"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// Unit specs: the request the brain encodes, read as the server reads it.
var _ = Describe("Encoding a chat request", func() {
	brain := &OllamaBrain{model: "qwen3:8b", contextWindow: 8192}

	It("sends the system and user messages, the schema and the keep-alive", func() {
		schema := json.RawMessage(`{"type":"object"}`)
		encoded, err := json.Marshal(brain.chatRequest(&Prompt{System: "label spans", User: "candidates", Schema: schema, KeepAlive: 150 * time.Second}))
		Expect(err).NotTo(HaveOccurred())
		Expect(string(encoded)).To(Equal(`{"model":"qwen3:8b","messages":[{"role":"system","content":"label spans"},{"role":"user","content":"candidates"}],"stream":false,"think":false,"format":{"type":"object"},"keep_alive":"2m30s","options":{"temperature":0,"num_ctx":8192}}`))
	})

	It("leaves out the system message, the schema and the keep-alive when the prompt has none", func() {
		encoded, err := json.Marshal(brain.chatRequest(&Prompt{User: "free text"}))
		Expect(err).NotTo(HaveOccurred())
		Expect(string(encoded)).To(Equal(`{"model":"qwen3:8b","messages":[{"role":"user","content":"free text"}],"stream":false,"think":false,"options":{"temperature":0,"num_ctx":8192}}`))
	})

	It("renders a keep-alive the way the server parses it", func() {
		Expect(keepAlive(0)).To(BeEmpty())
		Expect(keepAlive(-time.Second)).To(BeEmpty())
		Expect(keepAlive(90 * time.Second)).To(Equal("1m30s"))
	})

	It("reads the server's error text out of a refusal", func() {
		Expect(serverMessage([]byte(`{"error":"model 'qwen3:8b' not found"}`))).To(Equal("model 'qwen3:8b' not found"))
		Expect(serverMessage([]byte("  502 Bad Gateway\n"))).To(Equal("502 Bad Gateway"))
	})
})
