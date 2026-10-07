// Copyright 2026 Candace Labs

package sessiongate_test

import (
	"encoding/json"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/services/harness/session"
	"github.com/candacelabs/csf/services/harness/sessiongate"
)

var _ = Describe("IsSearchCall", func() {
	bash := func(command string) session.ToolUse {
		input, err := json.Marshal(map[string]string{"command": command, "description": "probe"})
		Expect(err).NotTo(HaveOccurred())
		return session.ToolUse{ID: "toolu_01", Name: session.ToolBash, Input: input}
	}

	DescribeTable("classifies a logged tool call",
		func(tool session.ToolUse, search bool) {
			Expect(sessiongate.IsSearchCall(tool)).To(Equal(search))
		},
		Entry("Grep", session.ToolUse{Name: session.ToolGrep, Input: json.RawMessage(`{"pattern":"Rule","path":"/src","output_mode":"content","-n":true}`)}, true),
		Entry("Glob", session.ToolUse{Name: session.ToolGlob, Input: json.RawMessage(`{"pattern":"**/*.go"}`)}, true),
		Entry("Read", session.ToolUse{Name: "Read", Input: json.RawMessage(`{"file_path":"/src/gate.go"}`)}, false),
		Entry("grep", bash(`grep -rn RuleSearchChain services`), true),
		Entry("egrep", bash(`egrep 'a|b' notes.txt`), true),
		Entry("fgrep", bash(`fgrep -l x *.go`), true),
		Entry("rg in a pipeline", bash(`git diff | rg -n TODO`), true),
		Entry("ag", bash(`ag Search`), true),
		Entry("ack", bash(`ack Search`), true),
		Entry("find after &&", bash(`cd /src && find . -name BUILD.bazel`), true),
		Entry("fd", bash(`fd -e go search`), true),
		Entry("rg inside bash -c", bash(`bash -c 'rg -n foo'`), true),
		Entry("grep behind env", bash(`env LC_ALL=C grep -c x f`), true),
		Entry("csf search", bash(`csf search "the wait gate's shell walk"`), false),
		Entry("csf search piped to grep", bash(`csf search "wait gate" | grep shell.go`), false),
		Entry("grep as quoted text", bash(`echo "grep foo"`), false),
		Entry("git grep", bash(`git grep foo`), false),
		Entry("an ordinary command", bash(`go test ./...`), false),
		Entry("a command the shell parser cannot read", bash(`while true; do`), false),
		Entry("an input that is not an object", session.ToolUse{Name: session.ToolGrep, Input: json.RawMessage(`"x"`)}, false),
	)
})
