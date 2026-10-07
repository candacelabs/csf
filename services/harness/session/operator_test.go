// Copyright 2026 Candace Labs

package session_test

import (
	"encoding/json"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/services/harness/session"
)

var _ = Describe("InstallOperatorGates", func() {
	var home string

	BeforeEach(func() {
		home = GinkgoT().TempDir()
	})

	claudeSettings := func() map[string]any {
		content, err := os.ReadFile(filepath.Join(home, ".claude", "settings.json"))
		Expect(err).NotTo(HaveOccurred())
		// Claude Code's user settings are an object of Claude Code's shape.
		settings := map[string]any{}
		Expect(json.Unmarshal(content, &settings)).To(Succeed())
		return settings
	}
	gateHook := func() map[string]any {
		return map[string]any{
			"matcher": "Bash",
			"hooks":   []any{map[string]any{"type": "command", "command": "/opt/csf/harness gate PreToolUse " + home, "timeout": 120.0}},
		}
	}

	It("hooks both executors' own sessions up to the wait gate, called with no run", func() {
		written, err := session.InstallOperatorGates(home, []string{gateBinary, gateVerb})
		Expect(err).NotTo(HaveOccurred())

		claude := filepath.Join(home, ".claude", "settings.json")
		copilot := filepath.Join(home, ".copilot", "hooks", "csf-gates.json")
		Expect(written).To(Equal([]string{claude, copilot}))
		Expect(claudeSettings()).To(Equal(map[string]any{"hooks": map[string]any{"PreToolUse": []any{gateHook()}}}))
		content, err := os.ReadFile(copilot)
		Expect(err).NotTo(HaveOccurred())
		hooks := map[string]any{}
		Expect(json.Unmarshal(content, &hooks)).To(Succeed())
		Expect(hooks).To(Equal(map[string]any{"hooks": map[string]any{"PreToolUse": []any{gateHook()}}}))
	})

	It("keeps every other setting and hook, and a second install changes nothing", func() {
		Expect(os.MkdirAll(filepath.Join(home, ".claude"), 0o700)).To(Succeed())
		existing := `{"model":"opus","hooks":{"PreToolUse":[{"matcher":"Edit","hooks":[{"type":"command","command":"lint"}]}],"Stop":[]}}`
		Expect(os.WriteFile(filepath.Join(home, ".claude", "settings.json"), []byte(existing), 0o600)).To(Succeed())

		_, err := session.InstallOperatorGates(home, []string{gateBinary, gateVerb})
		Expect(err).NotTo(HaveOccurred())
		first := claudeSettings()
		_, err = session.InstallOperatorGates(home, []string{gateBinary, gateVerb})
		Expect(err).NotTo(HaveOccurred())

		Expect(claudeSettings()).To(Equal(first))
		Expect(first).To(Equal(map[string]any{
			"model": "opus",
			"hooks": map[string]any{
				"PreToolUse": []any{
					map[string]any{"matcher": "Edit", "hooks": []any{map[string]any{"type": "command", "command": "lint"}}},
					gateHook(),
				},
				"Stop": []any{},
			},
		}))
	})

	DescribeTable("refuses what it cannot install, with the defined error",
		func(expected error, prepare func(home string) (string, []string)) {
			target, command := prepare(home)
			_, err := session.InstallOperatorGates(target, command)
			Expect(err).To(MatchError(expected))
		},
		Entry("a relative home", session.ErrInvalidOption, func(home string) (string, []string) { return "home", []string{gateBinary} }),
		Entry("no gate command", session.ErrNoGateCommand, func(home string) (string, []string) { return home, nil }),
		Entry("user settings that are not an object", session.ErrUserSettings, func(home string) (string, []string) {
			Expect(os.MkdirAll(filepath.Join(home, ".claude"), 0o700)).To(Succeed())
			Expect(os.WriteFile(filepath.Join(home, ".claude", "settings.json"), []byte(`[]`), 0o600)).To(Succeed())
			return home, []string{gateBinary}
		}),
	)
})
