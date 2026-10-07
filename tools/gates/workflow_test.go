package gates_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gopkg.in/yaml.v3"
)

const (
	houseLintWorkflow = "house-lint.yml"
	ontologyWorkflow  = "ontology-alignment.yml"
	acceptanceLabel   = "ontology-regression-accepted"
	// Private staging allocates no hosted runner (.github/workflows/README.md);
	// every job that would says so in its condition.
	visibilityGuard = "github.event.repository.private == false"
)

// The subset of a workflow these specs read. Unknown keys are ignored, so a
// step or job can grow without touching this shape.
type workflow struct {
	On          map[string]trigger `yaml:"on"`
	Permissions map[string]string  `yaml:"permissions"`
	Jobs        map[string]job     `yaml:"jobs"`
}

type trigger struct {
	Branches    []string `yaml:"branches"`
	Paths       []string `yaml:"paths"`
	PathsIgnore []string `yaml:"paths-ignore"`
	Types       []string `yaml:"types"`
}

type job struct {
	Name     string            `yaml:"name"`
	Needs    jobNames          `yaml:"needs"`
	If       string            `yaml:"if"`
	RunsOn   string            `yaml:"runs-on"`
	Timeout  int               `yaml:"timeout-minutes"`
	Env      map[string]string `yaml:"env"`
	Outputs  map[string]string `yaml:"outputs"`
	Strategy strategy          `yaml:"strategy"`
	Steps    []step            `yaml:"steps"`
}

type strategy struct {
	FailFast *bool  `yaml:"fail-fast"`
	Matrix   string `yaml:"matrix"`
}

type step struct {
	Name            string            `yaml:"name"`
	ID              string            `yaml:"id"`
	If              string            `yaml:"if"`
	Uses            string            `yaml:"uses"`
	Run             string            `yaml:"run"`
	With            map[string]string `yaml:"with"`
	Env             map[string]string `yaml:"env"`
	ContinueOnError bool              `yaml:"continue-on-error"`
}

// jobNames reads `needs` whether it names one job or lists several.
type jobNames []string

func (names *jobNames) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind == yaml.ScalarNode {
		*names = jobNames{node.Value}
		return nil
	}
	var listed []string
	if err := node.Decode(&listed); err != nil {
		return err
	}
	*names = jobNames(listed)
	return nil
}

// repositoryRoot is two directories up from this package. `go test` runs with
// the package directory as its working directory, and this target is tagged
// manual in Bazel (tools/bazel-manual-tests.txt) because it reads the tree.
func repositoryRoot() string {
	GinkgoHelper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	Expect(err).NotTo(HaveOccurred())
	Expect(filepath.Join(root, "MODULE.bazel")).To(BeAnExistingFile())
	return root
}

func readWorkflow(name string) workflow {
	GinkgoHelper()
	content, err := os.ReadFile(filepath.Join(repositoryRoot(), ".github", "workflows", name))
	Expect(err).NotTo(HaveOccurred())
	var document workflow
	Expect(yaml.Unmarshal(content, &document)).To(Succeed())
	return document
}

func stepNamed(definition job, name string) step {
	GinkgoHelper()
	for _, candidate := range definition.Steps {
		if candidate.Name == name {
			return candidate
		}
	}
	Fail("no step named " + name)
	return step{}
}

func stepIndex(definition job, name string) int {
	GinkgoHelper()
	for index, candidate := range definition.Steps {
		if candidate.Name == name {
			return index
		}
	}
	Fail("no step named " + name)
	return -1
}

// githubGlob follows GitHub's path-filter semantics: `**` crosses directory
// boundaries and `*` does not.
func githubGlob(pattern string) *regexp.Regexp {
	var expression strings.Builder
	for index := 0; index < len(pattern); {
		switch {
		case strings.HasPrefix(pattern[index:], "**/"):
			expression.WriteString("(?:.*/)?")
			index += 3
		case strings.HasPrefix(pattern[index:], "**"):
			expression.WriteString(".*")
			index += 2
		case pattern[index] == '*':
			expression.WriteString("[^/]*")
			index++
		default:
			expression.WriteString(regexp.QuoteMeta(pattern[index : index+1]))
			index++
		}
	}
	return regexp.MustCompile(`\A` + expression.String() + `\z`)
}

// One representative per input the score reads (tools/house_lint/score.ml):
// the native corpus is tracked Go and Python anywhere; the dependency rule
// reads go.mod and go.sum; the vocabulary lint reads every README.md and the
// generated glossary; csfc and the language generator read csf/; the launcher,
// the runner, the Gorilla checker it links and the Bazel graph build the
// checkers; the inventory honours .gitignore; the workflow calls the
// toolchain action.
var scoreInputs = []string{
	"csf/compiler/language/architecture.csf",
	"csf/architecture/architecture.csf",
	"csf/architecture/generated/csf_architecture_cgen.ml",
	"csf/compiler/architecture/compiler.ml",
	"csf/compiler/language/compiler.ml",
	"csf/docs/generated/ontology_cgen.md",
	"docs/GLOSSARY.md",
	"runtime/host.go",
	"io/tier.go",
	"app/csf/native/assemble.py",
	"README.md",
	"csf/README.md",
	"go.mod",
	"go.sum",
	".gitignore",
	"tools/house_lint/alignment.ml",
	"tools/house_lint/placement.ml",
	"tools/house_lint/policy.ml",
	"tools/gorilla_mux_lint/checker.ml",
	"tools/ontology-score.sh",
	"tools/bazel.sh",
	"bazel/execution_image.txt",
	"BUILD.bazel",
	"MODULE.bazel",
	"MODULE.bazel.lock",
	".bazelrc",
	".bazelversion",
	".bazelignore",
	".github/workflows/ontology-alignment.yml",
	".github/actions/ocaml-toolchain/action.yml",
}

func expectBoundedAndReadOnly(document workflow) {
	GinkgoHelper()
	Expect(document.Permissions).To(Equal(map[string]string{"contents": "read"}))
	for name, definition := range document.Jobs {
		Expect(definition.Timeout).To(BeNumerically(">", 0), name)
		Expect(definition.Timeout).To(BeNumerically("<=", 30), name)
		Expect(definition.RunsOn).To(Equal("ubuntu-24.04"), name)
		Expect(definition.If).To(ContainSubstring(visibilityGuard), name)
		for _, candidate := range definition.Steps {
			if strings.HasPrefix(candidate.Uses, "actions/") {
				Expect(candidate.Uses).To(MatchRegexp(`@[0-9a-f]{40}`), name)
			}
		}
	}
}

var _ = Describe("the glob helper", func() {
	It("matches the way GitHub path filters do", func() {
		Expect(githubGlob("**/*.go").MatchString("a/b/c.go")).To(BeTrue())
		Expect(githubGlob("**/*.go").MatchString("c.go")).To(BeTrue())
		Expect(githubGlob("csf/**").MatchString("csf/x/y.txt")).To(BeTrue())
		Expect(githubGlob("tools/*.sh").MatchString("tools/x/y.sh")).To(BeFalse())
		Expect(githubGlob("docs/GLOSSARY.md").MatchString("docs/GLOSSARY.mdx")).To(BeFalse())
	})
})

var _ = Describe("the house lint workflow", func() {
	var document workflow

	BeforeEach(func() {
		document = readWorkflow(houseLintWorkflow)
	})

	It("runs on every pull request and every main or release push with no path filter", func() {
		pull, listens := document.On["pull_request"]
		Expect(listens).To(BeTrue())
		Expect(pull.Paths).To(BeEmpty())
		Expect(pull.PathsIgnore).To(BeEmpty())
		push, listens := document.On["push"]
		Expect(listens).To(BeTrue())
		Expect(push.Branches).To(Equal([]string{"main", "release/**"}))
		Expect(push.Paths).To(BeEmpty())
		Expect(push.PathsIgnore).To(BeEmpty())
	})

	It("is bounded, read-only and hosted only for a public repository", func() {
		expectBoundedAndReadOnly(document)
	})

	It("builds and tests the checker before any lane scans with it", func() {
		native := document.Jobs["native"]
		Expect(native.Needs).To(Equal(jobNames{"ocaml_toolchain"}))
		build := stepNamed(native, "Build the checker and run every native regression")
		Expect(build.Run).To(Equal("bash tools/check-house-lint.sh --test --build-only"))
		inventory := stepNamed(native, "Prepare the exact checker and its complete lane inventory")
		Expect(inventory.ID).To(Equal("inventory"))
		Expect(inventory.Run).To(ContainSubstring(`"$output/house-lint" --ci-lanes`))
		Expect(inventory.Run).To(ContainSubstring("sha256sum house-lint source-revision > SHA256SUMS"))
		Expect(native.Outputs).To(HaveKeyWithValue("matrix", "${{ steps.inventory.outputs.matrix }}"))
		handoff := stepNamed(native, "Hand the tested checker to the lanes")
		Expect(handoff.With).To(HaveKeyWithValue("name", "house-lint-native"))
		Expect(handoff.With).To(HaveKeyWithValue("if-no-files-found", "error"))
		Expect(stepIndex(native, build.Name)).To(BeNumerically("<", stepIndex(native, inventory.Name)))
		Expect(stepIndex(native, inventory.Name)).To(BeNumerically("<", stepIndex(native, handoff.Name)))
	})

	It("verifies the handed-over checker in every registered lane", func() {
		scan := document.Jobs["scan"]
		Expect(scan.Needs).To(Equal(jobNames{"native"}))
		Expect(scan.Strategy.FailFast).To(HaveValue(BeFalse()))
		Expect(scan.Strategy.Matrix).To(Equal("${{ fromJSON(needs.native.outputs.matrix) }}"))
		restore := stepNamed(scan, "Restore the tested checker for this revision")
		Expect(restore.With).To(HaveKeyWithValue("name", "house-lint-native"))
		verify := stepNamed(scan, "Verify the checker and run the selected lane")
		Expect(verify.Env).To(HaveKeyWithValue("HOUSE_LINT_LANE", "${{ matrix.lane }}"))
		Expect(verify.Run).To(ContainSubstring(`test "$(cat "$checker/source-revision")" = "$GITHUB_SHA"`))
		Expect(verify.Run).To(ContainSubstring("sha256sum --check --strict SHA256SUMS"))
		Expect(verify.Run).To(ContainSubstring(
			`"$checker/house-lint" --root "$PWD" --lane "$HOUSE_LINT_LANE" --summary house-lint-report.md`))
		Expect(verify.Run).NotTo(ContainSubstring("|| true"))
		Expect(verify.ContinueOnError).To(BeFalse())
		report := stepNamed(scan, "Retain the lane report and specialist logs")
		Expect(report.If).To(Equal("always()"))
		Expect(report.ContinueOnError).To(BeTrue())
	})

	It("requires the checker and every lane in one aggregate check", func() {
		aggregate := document.Jobs["house_lint"]
		Expect(aggregate.Name).To(Equal("House lint (mandatory and advisory)"))
		Expect(aggregate.Needs).To(ConsistOf("native", "scan"))
		Expect(aggregate.If).To(ContainSubstring("always()"))
		Expect(aggregate.Env).To(HaveKeyWithValue("NATIVE_RESULT", "${{ needs.native.result }}"))
		Expect(aggregate.Env).To(HaveKeyWithValue("SCAN_RESULT", "${{ needs.scan.result }}"))
		Expect(aggregate.Steps).To(HaveLen(1))
		guard := aggregate.Steps[0].Run
		Expect(guard).To(ContainSubstring(`"native=$NATIVE_RESULT" "scan=$SCAN_RESULT"`))
		Expect(guard).To(ContainSubstring("*=success) ;;"))
		Expect(guard).To(ContainSubstring("status=1"))
		Expect(guard).To(ContainSubstring(`exit "$status"`))
	})
})

var _ = Describe("the ontology alignment workflow", func() {
	var document workflow

	BeforeEach(func() {
		document = readWorkflow(ontologyWorkflow)
	})

	It("records every main and release push with no path filter", func() {
		push := document.On["push"]
		Expect(push.Branches).To(Equal([]string{"main", "release/**"}))
		Expect(push.Paths).To(BeEmpty())
		Expect(push.PathsIgnore).To(BeEmpty())
		score := document.Jobs["score"]
		Expect(score.If).To(ContainSubstring("github.event_name != 'pull_request'"))
		measure := stepNamed(score, "Measure this commit")
		Expect(measure.Run).To(Equal("bash tools/ontology-score.sh --receipt ontology-alignment"))
		Expect(measure.ContinueOnError).To(BeFalse())
		Expect(stepNamed(score, "Retain the receipt").If).To(Equal("always()"))
	})

	It("names every input the score reads in the pull request filter", func() {
		pull := document.On["pull_request"]
		Expect(pull.PathsIgnore).To(BeEmpty())
		Expect(pull.Paths).NotTo(BeEmpty())
		patterns := make([]*regexp.Regexp, 0, len(pull.Paths))
		for _, pattern := range pull.Paths {
			patterns = append(patterns, githubGlob(pattern))
		}
		root := repositoryRoot()
		var unfiltered []string
		for _, input := range scoreInputs {
			Expect(filepath.Join(root, input)).To(BeAnExistingFile(), "representative score input is gone")
			covered := false
			for _, pattern := range patterns {
				if pattern.MatchString(input) {
					covered = true
					break
				}
			}
			if !covered {
				unfiltered = append(unfiltered, input)
			}
		}
		Expect(unfiltered).To(BeEmpty(), "the ratchet would stay green by never looking at these inputs")
	})

	It("reruns the ratchet when the acceptance label changes", func() {
		Expect(document.On["pull_request"].Types).To(ContainElements("opened", "synchronize", "reopened", "labeled", "unlabeled"))
	})

	It("compares the head with its merge base and fails on a regression", func() {
		ratchet := document.Jobs["ratchet"]
		Expect(ratchet.If).To(ContainSubstring("github.event_name == 'pull_request'"))
		checkout := ratchet.Steps[0]
		Expect(checkout.With).To(HaveKeyWithValue("ref", "${{ github.event.pull_request.head.sha }}"))
		// The merge base is computed from history; a shallow clone has none.
		Expect(checkout.With).To(HaveKeyWithValue("fetch-depth", "0"))
		compare := stepNamed(ratchet, "Compare the head with its merge base")
		Expect(compare.Run).To(ContainSubstring(`bash tools/ontology-score.sh --ratchet --base "origin/$BASE_REF"`))
		Expect(ratchet.Env).To(HaveKeyWithValue("BASE_REF", "${{ github.base_ref }}"))
		Expect(compare.Run).To(ContainSubstring(`>>"$GITHUB_STEP_SUMMARY"`))
		Expect(compare.Run).To(ContainSubstring(`exit "$status"`))
		Expect(compare.Run).NotTo(ContainSubstring("|| true"))
		Expect(compare.ContinueOnError).To(BeFalse())
	})

	It("accepts a regression only through the label", func() {
		ratchet := document.Jobs["ratchet"]
		Expect(ratchet.Env).To(HaveKeyWithValue("ACCEPTED",
			"${{ contains(github.event.pull_request.labels.*.name, '"+acceptanceLabel+"') }}"))
		compare := stepNamed(ratchet, "Compare the head with its merge base")
		Expect(compare.Run).To(ContainSubstring(`if [ "$ACCEPTED" = true ]; then accept=(--accept-regression); fi`))
		Expect(strings.Count(compare.Run, "--accept-regression")).To(Equal(1))
	})

	It("retains both receipts even when the ratchet fails", func() {
		upload := stepNamed(document.Jobs["ratchet"], "Retain both receipts")
		Expect(upload.If).To(Equal("always()"))
		Expect(upload.With["path"]).To(ContainSubstring("ontology-ratchet/"))
	})

	It("is bounded, read-only and hosted only for a public repository", func() {
		expectBoundedAndReadOnly(document)
	})
})
