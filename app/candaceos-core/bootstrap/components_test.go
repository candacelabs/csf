package bootstrap

// These specs are in-package because the built-in bring-up graph, the assembly
// driver, and the reporter are deliberately unexported: an embedding
// repository can reach none of them. They assert Core's frozen ordering
// without a control-plane database.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"strings"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/pkg/redact"
	"github.com/candacelabs/csf/pkg/telemetry"
	"github.com/candacelabs/csf/services/candaceos/component"
)

func TestBootstrapComponents(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "CandaceOS Bootstrap Component Suite")
}

var builtInOrder = []string{
	"configuration", "database", "database-recovery", "fleet",
	"node-agent", "reconciler", "harness", "runtime", "http",
}

type journal struct {
	entries []string
}

func (record *journal) note(entry string) { record.entries = append(record.entries, entry) }

type logLine struct {
	Severity   string            `json:"severity"`
	Event      string            `json:"event"`
	Message    string            `json:"message"`
	Attributes map[string]string `json:"attributes"`
}

func testReporter(destination io.Writer) *reporter {
	GinkgoHelper()
	logger, err := telemetry.NewJSONLLogger(destination, "candaceos-core", "runtime")
	Expect(err).NotTo(HaveOccurred())
	return &reporter{logger: logger}
}

func decodeLines(buffer *bytes.Buffer) []logLine {
	GinkgoHelper()
	lines := []logLine{}
	for _, raw := range strings.Split(strings.TrimSpace(buffer.String()), "\n") {
		if raw == "" {
			continue
		}
		line := logLine{}
		Expect(json.Unmarshal([]byte(raw), &line)).To(Succeed())
		lines = append(lines, line)
	}
	return lines
}

func definitionNames(definitions []*component.Definition) []string {
	names := make([]string, 0, len(definitions))
	for _, definition := range definitions {
		names = append(names, definition.Name())
	}
	return names
}

func inertDefinition(
	name string,
	requirements ...*component.Definition,
) *component.Definition {
	GinkgoHelper()
	definition, err := component.New(
		name,
		component.WithAssemble(func(ctx context.Context, capabilities component.ICapabilities) error {
			return nil
		}),
		component.WithRequires(requirements...),
	)
	Expect(err).NotTo(HaveOccurred())
	return definition
}

// lifecycleDefinition records every hook it runs and reports the supplied
// failures, so a spec can assert both order and error accumulation.
func lifecycleDefinition(
	name string,
	record *journal,
	assembleErr error,
	startErr error,
	stopErr error,
) *component.Definition {
	GinkgoHelper()
	definition, err := component.New(
		name,
		component.WithAssemble(func(ctx context.Context, capabilities component.ICapabilities) error {
			record.note("assemble " + name)
			return assembleErr
		}),
		component.WithStart(func(ctx context.Context) error {
			record.note("start " + name)
			return startErr
		}),
		component.WithStop(func(ctx context.Context) error {
			record.note("stop " + name)
			return stopErr
		}),
	)
	Expect(err).NotTo(HaveOccurred())
	return definition
}

var _ = Describe("Built-in bring-up graph", func() {
	It("resolves Core's frozen startup sequence", func() {
		assembly := &coreAssembly{}
		declared, err := assembly.definitions()
		Expect(err).NotTo(HaveOccurred())
		Expect(definitionNames(declared)).To(Equal(builtInOrder))

		resolved, err := component.Order(declared...)
		Expect(err).NotTo(HaveOccurred())
		Expect(definitionNames(resolved)).To(Equal(definitionNames(declared)))
		Expect(definitionNames(resolved)).To(Equal(builtInOrder))
	})

	It("declares no built-in step twice", func() {
		assembly := &coreAssembly{}
		declared, err := assembly.definitions()
		Expect(err).NotTo(HaveOccurred())
		Expect(builtInOrder).To(HaveLen(len(declared)))
	})
})

var _ = Describe("Registered components", func() {
	It("resolves between the reconciler and the harness in registration order", func() {
		assembly := &coreAssembly{settings: assemblyOptions{
			components: []*component.Definition{inertDefinition("alpha"), inertDefinition("beta")},
		}}
		resolved, err := assembly.resolve()
		Expect(err).NotTo(HaveOccurred())
		Expect(definitionNames(resolved)).To(Equal([]string{
			"configuration", "database", "database-recovery", "fleet",
			"node-agent", "reconciler", "alpha", "beta", "harness", "runtime", "http",
		}))
	})

	It("orders a declared requirement ahead of an earlier registration", func() {
		beta := inertDefinition("beta")
		alpha := inertDefinition("alpha", beta)
		assembly := &coreAssembly{settings: assemblyOptions{
			components: []*component.Definition{alpha, beta},
		}}
		resolved, err := assembly.resolve()
		Expect(err).NotTo(HaveOccurred())
		Expect(definitionNames(resolved)).To(Equal([]string{
			"configuration", "database", "database-recovery", "fleet",
			"node-agent", "reconciler", "beta", "alpha", "harness", "runtime", "http",
		}))
	})

	It("rejects a nil registration", func() {
		settings, err := applyOptions([]Option{WithComponent(nil)})
		Expect(settings.components).To(BeEmpty())
		Expect(err).To(MatchError(component.ErrNilDefinition))
	})

	It("rejects two registrations sharing one name", func(ctx SpecContext) {
		core, err := AssembleCore(
			ctx, "component-test",
			WithComponent(inertDefinition("alpha")),
			WithComponent(inertDefinition("alpha")),
		)
		Expect(core).To(BeNil())
		Expect(err).To(MatchError(component.ErrDuplicateName))
	})

	It("rejects a registration that shadows a built-in step", func(ctx SpecContext) {
		core, err := AssembleCore(ctx, "component-test", WithComponent(inertDefinition("harness")))
		Expect(core).To(BeNil())
		Expect(err).To(MatchError(component.ErrDuplicateName))
	})

	It("rejects a requirement that was never registered", func(ctx SpecContext) {
		absent := inertDefinition("absent")
		core, err := AssembleCore(
			ctx, "component-test",
			WithComponent(inertDefinition("dependent", absent)),
		)
		Expect(core).To(BeNil())
		Expect(err).To(MatchError(component.ErrMissingRequirement))
		Expect(err.Error()).To(ContainSubstring(`"dependent" requires "absent"`))
	})
})

var _ = Describe("Component assembly", func() {
	It("records only registered components on Core, in resolved order", func(ctx SpecContext) {
		record := &journal{}
		alpha := lifecycleDefinition("alpha", record, nil, nil, nil)
		beta := lifecycleDefinition("beta", record, nil, nil, nil)
		assembly := &coreAssembly{
			reporter: testReporter(&bytes.Buffer{}),
			settings: assemblyOptions{components: []*component.Definition{alpha, beta}},
		}
		assembly.core = &Core{reporter: assembly.reporter}

		Expect(assembly.assemble(ctx, alpha)).To(Succeed())
		Expect(assembly.assemble(ctx, beta)).To(Succeed())
		Expect(record.entries).To(Equal([]string{"assemble alpha", "assemble beta"}))
		Expect(definitionNames(assembly.core.extensions)).To(Equal([]string{"alpha", "beta"}))
	})

	It("attributes an assembly failure to the extension boundary", func(ctx SpecContext) {
		buffer := &bytes.Buffer{}
		record := &journal{}
		failure := errors.New("alpha could not assemble")
		alpha := lifecycleDefinition("alpha", record, failure, nil, nil)
		assembly := &coreAssembly{
			reporter: testReporter(buffer),
			settings: assemblyOptions{components: []*component.Definition{alpha}},
		}
		assembly.core = &Core{reporter: assembly.reporter}

		Expect(assembly.assemble(ctx, alpha)).To(MatchError(failure))
		Expect(assembly.core.extensions).To(BeEmpty())

		lines := decodeLines(buffer)
		Expect(lines).To(HaveLen(1))
		Expect(lines[0].Event).To(Equal("core.startup.failed"))
		Expect(lines[0].Severity).To(Equal("SEVERITY_FATAL"))
		Expect(lines[0].Attributes).To(Equal(map[string]string{
			"component": "extension",
			"extension": "alpha",
			"error":     failure.Error(),
		}))
	})

	It("keeps a component assembled before its own failure releasable", func(ctx SpecContext) {
		record := &journal{}
		failure := errors.New("beta could not assemble")
		alpha := lifecycleDefinition("alpha", record, nil, nil, nil)
		beta := lifecycleDefinition("beta", record, failure, nil, nil)
		assembly := &coreAssembly{
			reporter: testReporter(&bytes.Buffer{}),
			settings: assemblyOptions{components: []*component.Definition{alpha, beta}},
		}
		assembly.core = &Core{reporter: assembly.reporter}

		Expect(assembly.assemble(ctx, alpha)).To(Succeed())
		Expect(assembly.assemble(ctx, beta)).To(MatchError(failure))
		Expect(record.entries).To(Equal([]string{"assemble alpha", "assemble beta", "stop alpha"}))
	})
})

var _ = Describe("Component lifecycle in Run", func() {
	It("starts components before the controller and stops them in reverse", func(ctx SpecContext) {
		buffer := &bytes.Buffer{}
		record := &journal{}
		failure := errors.New("beta could not start")
		alpha := lifecycleDefinition("alpha", record, nil, nil, nil)
		beta := lifecycleDefinition("beta", record, nil, failure, nil)
		core := &Core{
			reporter:   testReporter(buffer),
			extensions: []*component.Definition{alpha, beta},
		}

		Expect(core.Run(ctx)).To(MatchError(failure))
		Expect(record.entries).To(Equal([]string{
			"start alpha", "start beta", "stop beta", "stop alpha",
		}))

		lines := decodeLines(buffer)
		Expect(lines).To(HaveLen(1))
		Expect(lines[0].Attributes).To(HaveKeyWithValue("component", "extension"))
		Expect(lines[0].Attributes).To(HaveKeyWithValue("extension", "beta"))
		Expect(core.Run(ctx)).To(MatchError("CandaceOS Core is closed"))
	})

	It("stops every assembled component even when one Stop fails", func() {
		record := &journal{}
		first := lifecycleDefinition("first", record, nil, nil, errors.New("first stop failed"))
		second := lifecycleDefinition("second", record, nil, nil, errors.New("second stop failed"))
		core := &Core{
			reporter:   testReporter(&bytes.Buffer{}),
			extensions: []*component.Definition{first, second},
		}

		err := core.Close()
		Expect(err).To(MatchError(ContainSubstring(`stopping composed component "first": first stop failed`)))
		Expect(err).To(MatchError(ContainSubstring(`stopping composed component "second": second stop failed`)))
		Expect(record.entries).To(Equal([]string{"stop second", "stop first"}))
		Expect(core.Close()).To(Equal(err), "Close must be idempotent")
	})

	It("reports no extension order when nothing is registered", func() {
		Expect((&Core{}).extensionOrder()).To(BeEmpty())
	})

	It("bounds the extension order attribute for any registered set", func() {
		names := make([]string, 0, 20)
		extensions := make([]*component.Definition, 0, 20)
		for index := 0; index < 20; index++ {
			name := "x" + strconv.Itoa(index) + strings.Repeat("a", component.MaxNameBytes-2-len(strconv.Itoa(index)))
			names = append(names, name)
			extensions = append(extensions, inertDefinition(name))
		}
		order := (&Core{extensions: extensions}).extensionOrder()

		Expect(len(order)).To(BeNumerically("<=", telemetry.MaxAttributeValueBytes))
		Expect(order).To(HavePrefix(names[0] + "," + names[1]))
		Expect(order).To(MatchRegexp(`,\+\d+$`), "names beyond the bound collapse to a count tail")

		short := (&Core{extensions: extensions[:2]}).extensionOrder()
		Expect(short).To(Equal(names[0] + "," + names[1]))
	})
})

var _ = Describe("Teardown attribution", func() {
	It("keeps the harness close event for a release with no component failures", func() {
		buffer := &bytes.Buffer{}
		core := &Core{reporter: testReporter(buffer)}

		err := core.reportReleaseFailure(context.Background(), errors.New("harness jammed"))

		Expect(err).To(MatchError(ContainSubstring("harness jammed")))
		lines := decodeLines(buffer)
		Expect(lines).To(HaveLen(1))
		Expect(lines[0].Event).To(Equal("core.harness.close_failed"))
		Expect(lines[0].Attributes).NotTo(HaveKey("extension"))
	})

	It("attributes a component stop failure to the component, not the harness", func() {
		buffer := &bytes.Buffer{}
		core := &Core{reporter: testReporter(buffer)}
		core.extensionStopFailures = []extensionStopFailure{
			{name: "steering-service", cause: errors.New("flush failed")},
		}

		err := core.reportReleaseFailure(context.Background(), errors.New("joined release error"))

		Expect(err).To(MatchError(ContainSubstring("flush failed")))
		lines := decodeLines(buffer)
		Expect(lines).To(HaveLen(1))
		Expect(lines[0].Event).To(Equal("core.shutdown.failed"))
		Expect(lines[0].Message).To(Equal("Stopping composed component"))
		Expect(lines[0].Attributes).To(HaveKeyWithValue("extension", "steering-service"))
	})

	It("reports a harness failure and a component failure to their own events", func() {
		buffer := &bytes.Buffer{}
		core := &Core{reporter: testReporter(buffer)}
		core.harnessCloseErr = errors.New("harness jammed")
		core.extensionStopFailures = []extensionStopFailure{
			{name: "steering-store", cause: errors.New("drain failed")},
		}

		err := core.reportReleaseFailure(context.Background(), errors.New("joined release error"))

		Expect(err).To(MatchError(ContainSubstring("harness jammed")))
		Expect(err).To(MatchError(ContainSubstring("drain failed")))
		lines := decodeLines(buffer)
		Expect(lines).To(HaveLen(2))
		Expect(lines[0].Event).To(Equal("core.harness.close_failed"))
		Expect(lines[0].Attributes).NotTo(HaveKey("extension"))
		Expect(lines[1].Event).To(Equal("core.shutdown.failed"))
		Expect(lines[1].Attributes).To(HaveKeyWithValue("extension", "steering-store"))
	})
})

var _ = Describe("Startup telemetry", func() {
	It("keeps the started event unchanged without registered components", func(ctx SpecContext) {
		buffer := &bytes.Buffer{}
		Expect(testReporter(buffer).started(ctx, ":8080", "demo", "demo/1", "v1", "")).To(Succeed())

		lines := decodeLines(buffer)
		Expect(lines).To(HaveLen(1))
		Expect(lines[0].Event).To(Equal("core.started"))
		Expect(lines[0].Attributes).To(Equal(map[string]string{
			"bind":                   ":8080",
			"harness_backend":        "demo",
			"harness_implementation": "demo/1",
			"version":                "v1",
		}))
	})

	It("adds the resolved order when components are registered", func(ctx SpecContext) {
		buffer := &bytes.Buffer{}
		core := &Core{extensions: []*component.Definition{
			inertDefinition("alpha"), inertDefinition("beta"),
		}}
		Expect(testReporter(buffer).started(
			ctx, ":8080", "demo", "demo/1", "v1", core.extensionOrder(),
		)).To(Succeed())

		lines := decodeLines(buffer)
		Expect(lines[0].Attributes).To(HaveKeyWithValue("extensions", "alpha,beta"))
	})
})

var _ = Describe("Component capabilities", func() {
	It("namespaces an event under the component name and redacts the message", func(ctx SpecContext) {
		buffer := &bytes.Buffer{}
		reporter := testReporter(buffer)
		reporter.redactor = redact.NewRedactor("s3cret")
		assembly := &coreAssembly{reporter: reporter}
		capabilities := componentCapabilities{assembly: assembly, name: "alpha"}

		Expect(capabilities.Log(ctx, "ready", "listening with s3cret")).To(Succeed())
		lines := decodeLines(buffer)
		Expect(lines).To(HaveLen(1))
		Expect(lines[0].Event).To(Equal("component.alpha.ready"))
		Expect(lines[0].Severity).To(Equal("SEVERITY_INFO"))
		Expect(lines[0].Message).To(Equal("listening with " + redact.Replacement))
	})

	It("keeps the message intact when the binary opted into PII", func(ctx SpecContext) {
		buffer := &bytes.Buffer{}
		reporter := testReporter(buffer)
		reporter.includePII = true
		reporter.redactor = redact.NewRedactor("s3cret")
		capabilities := componentCapabilities{
			assembly: &coreAssembly{reporter: reporter},
			name:     "alpha",
		}

		Expect(capabilities.Log(ctx, "ready", "listening with s3cret")).To(Succeed())
		Expect(decodeLines(buffer)[0].Message).To(Equal("listening with s3cret"))
	})

	DescribeTable(
		"rejects an event outside the documented bound",
		func(event string) {
			buffer := &bytes.Buffer{}
			capabilities := componentCapabilities{
				assembly: &coreAssembly{reporter: testReporter(buffer)},
				name:     "alpha",
			}
			Expect(capabilities.Log(context.Background(), event, "message")).
				To(MatchError(ContainSubstring("event must contain 1 to 48 bytes")))
			Expect(buffer.Len()).To(BeZero())
		},
		Entry("empty", ""),
		Entry("one byte past the bound", strings.Repeat("e", component.MaxEventBytes+1)),
	)

	It("keeps the namespaced event inside the telemetry event bound", func(ctx SpecContext) {
		buffer := &bytes.Buffer{}
		name := "a" + strings.Repeat("b", component.MaxNameBytes-1)
		capabilities := componentCapabilities{
			assembly: &coreAssembly{reporter: testReporter(buffer)},
			name:     name,
		}

		event := strings.Repeat("e", component.MaxEventBytes)
		Expect(capabilities.Log(ctx, event, "message")).To(Succeed())
		Expect(decodeLines(buffer)[0].Event).To(HaveLen(len("component.") + len(name) + 1 + len(event)))
	})
})
