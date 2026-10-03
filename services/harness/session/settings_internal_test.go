// Copyright 2026 Candace Labs

package session

import (
	"encoding/json"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"mvdan.cc/sh/v3/syntax"
)

var _ = Describe("session settings", func() {
	It("quotes every gate argument so the hook shell reads the same vector back", func() {
		gate := []string{"/opt/my tools/harness", "gate"}
		directory := "/state/it's here"
		content, err := sessionSettings(gate, directory)
		Expect(err).NotTo(HaveOccurred())
		settings := claudeSettings{}
		Expect(json.Unmarshal(content, &settings)).To(Succeed())
		for _, event := range []string{HookPreToolUse, HookPostToolUse} {
			Expect(settings.Hooks[event]).To(HaveLen(1))
			hook := settings.Hooks[event][0].Hooks[0]
			Expect(hook.Timeout).To(Equal(gateTimeouts[event]))
			file, err := syntax.NewParser().Parse(strings.NewReader(hook.Command), "")
			Expect(err).NotTo(HaveOccurred())
			call, ok := file.Stmts[0].Cmd.(*syntax.CallExpr)
			Expect(ok).To(BeTrue())
			var words []string
			for _, word := range call.Args {
				var text strings.Builder
				for _, part := range word.Parts {
					switch typed := part.(type) {
					case *syntax.Lit:
						text.WriteString(typed.Value)
					case *syntax.SglQuoted:
						text.WriteString(typed.Value)
					case *syntax.DblQuoted:
						for _, inner := range typed.Parts {
							text.WriteString(inner.(*syntax.Lit).Value)
						}
					}
				}
				words = append(words, text.String())
			}
			Expect(words).To(Equal([]string{gate[0], gate[1], event, directory}))
		}
	})

	It("gives the PreToolUse gate time to run the merge path's checks before a pull request is marked ready", func() {
		Expect(gateTimeouts[HookPreToolUse]).To(Equal(readyGateTimeoutSeconds))
		Expect(gateTimeouts[HookPostToolUse]).To(Equal(gateTimeoutSeconds))
		Expect(readyGateTimeoutSeconds).To(BeNumerically(">", gateTimeoutSeconds))
	})

	It("frames a prompt as one stream-json user message", func() {
		message, err := userMessage("say \"hi\"\nthen stop")
		Expect(err).NotTo(HaveOccurred())
		Expect(string(message)).To(Equal(`{"type":"user","message":{"role":"user","content":"say \"hi\"\nthen stop"}}`))
	})

	It("places each run under its assignment", func() {
		Expect(RunDirectory("/state", "0caf2d51-1ee7-468a-882f-ac478a437640")).To(Equal("/state/0caf2d51-1ee7-468a-882f-ac478a437640"))
	})
})
