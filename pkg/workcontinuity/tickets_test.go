package workcontinuity_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/pkg/workcontinuity"
	workv1 "github.com/candacelabs/csf/proto/candace/work/v1"
)

var _ = Describe("typed ticket publication", func() {
	It("renders the shared template with dependency conditions as data", func() {
		spec := workcontinuity.TicketTemplate()
		spec.Title = "Mount a task queue"
		spec.Goal = "Use mount for runtime composition."
		body, err := workcontinuity.RenderTicket(spec)
		Expect(err).NotTo(HaveOccurred())
		Expect(body).To(ContainSubstring("## Goal\n\nUse mount for runtime composition."))
		Expect(body).To(ContainSubstring("- [ ] Describe an observable completion criterion."))
		Expect(body).To(ContainSubstring("| https://github.com/example/project/issues/1 | Stack in a new worktree or wait for merge |"))
		Expect(body).To(ContainSubstring("1. Create a **new worktree** from its implementation branch and **stack these changes on top**."))
		Expect(body).To(ContainSubstring("2. Wait for its implementation to merge, then develop from the updated base."))
		Expect(body).NotTo(ContainSubstring("Blocked by"))
		Expect(strings.ToLower(body)).NotTo(ContainSubstring("do not begin"))
		spec.Dependencies[0].Condition = workv1.DependencyCondition_DEPENDENCY_CONDITION_IMPLEMENTATION_MERGED
		body, err = workcontinuity.RenderTicket(spec)
		Expect(err).NotTo(HaveOccurred())
		Expect(body).To(ContainSubstring("| Implementation merged |"))
		Expect(body).NotTo(ContainSubstring("Create a **new worktree**"))
		spec.Dependencies = nil
		body, err = workcontinuity.RenderTicket(spec)
		Expect(err).NotTo(HaveOccurred())
		Expect(body).NotTo(ContainSubstring("Dependencies"))
	})

	DescribeTable("rejects malformed author input before any GitHub call", func(change func(spec *workv1.TicketSpec)) {
		spec := workcontinuity.TicketTemplate()
		change(spec)
		source := workcontinuity.NewGitHubSource(func(ctx context.Context, args ...string) ([]byte, error) {
			Fail("invalid input must not contact GitHub")
			return nil, nil
		})
		receipt, err := source.CreateTicket(context.Background(), spec)
		Expect(err).To(HaveOccurred())
		Expect(receipt).To(BeNil())
	},
		Entry("unknown version", func(spec *workv1.TicketSpec) { spec.SchemaVersion = 2 }),
		Entry("wrong repository", func(spec *workv1.TicketSpec) { spec.Repository = "org/repo/issues/1" }),
		Entry("blank title", func(spec *workv1.TicketSpec) { spec.Title = " \t" }),
		Entry("multiline title", func(spec *workv1.TicketSpec) { spec.Title = "first\nsecond" }),
		Entry("blank goal", func(spec *workv1.TicketSpec) { spec.Goal = "\n " }),
		Entry("missing scope", func(spec *workv1.TicketSpec) { spec.Scope = nil }),
		Entry("blank acceptance", func(spec *workv1.TicketSpec) { spec.Acceptance = []string{" "} }),
		Entry("unknown condition", func(spec *workv1.TicketSpec) { spec.Dependencies[0].Condition = 99 }),
		Entry("missing dependency", func(spec *workv1.TicketSpec) { spec.Dependencies[0] = nil }),
		Entry("duplicate dependency", func(spec *workv1.TicketSpec) { spec.Dependencies = append(spec.Dependencies, spec.Dependencies[0]) }),
		Entry("case-insensitive duplicate", func(spec *workv1.TicketSpec) {
			spec.Dependencies = append(spec.Dependencies, &workv1.TicketDependency{
				IssueUrl:  "https://github.com/EXAMPLE/PROJECT/issues/1",
				Condition: workv1.DependencyCondition_DEPENDENCY_CONDITION_COMPLETED,
			})
		}),
		Entry("invalid dependency URL", func(spec *workv1.TicketSpec) { spec.Dependencies[0].IssueUrl += "?x=1" }),
		Entry("oversized body", func(spec *workv1.TicketSpec) {
			spec.Scope = make([]string, 10)
			for i := range spec.Scope {
				spec.Scope[i] = strings.Repeat("a", 8192)
			}
		}),
	)

	DescribeTable("preflights every dependency then creates and links using literal arguments", func(repository, dependencyURL string) {
		spec := workcontinuity.TicketTemplate()
		spec.Repository = repository
		spec.Dependencies[0].IssueUrl = dependencyURL
		spec.Dependencies[0].Condition = workv1.DependencyCondition_DEPENDENCY_CONDITION_IMPLEMENTATION_MERGED
		spec.Title = "Use `mount` $(literal); keep text"
		spec.Dependencies = append(spec.Dependencies, &workv1.TicketDependency{
			IssueUrl:  "https://github.com/other/project/issues/2",
			Condition: workv1.DependencyCondition_DEPENDENCY_CONDITION_COMPLETED,
		})
		body, err := workcontinuity.RenderTicket(spec)
		Expect(err).NotTo(HaveOccurred())
		calls := 0
		source := workcontinuity.NewGitHubSource(func(ctx context.Context, args ...string) ([]byte, error) {
			calls++
			switch calls {
			case 1:
				Expect(args).To(Equal([]string{"api", "repos/" + strings.TrimPrefix(dependencyURL, "https://github.com/")}))
				return []byte(`{"id":101,"number":1,"state":"open","html_url":"https://github.com/example/project/issues/1"}`), nil
			case 2:
				Expect(args).To(Equal([]string{"api", "repos/other/project/issues/2"}))
				return []byte(`{"id":102,"number":2,"state":"closed","html_url":"https://github.com/other/project/issues/2"}`), nil
			case 3:
				Expect(args).To(Equal([]string{"api", "repos/" + repository + "/issues", "--method", "POST", "--raw-field", "title=" + spec.Title, "--raw-field", "body=" + body}))
				return json.Marshal(map[string]any{"number": 3, "title": spec.Title, "body": body, "html_url": "https://github.com/example/project/issues/3"})
			case 4, 5:
				Expect(args).To(Equal([]string{"api", "repos/" + repository + "/issues/3/dependencies/blocked_by", "--method", "POST", "--field", fmt.Sprintf("issue_id=%d", calls+97)}))
				return []byte(`{}`), nil
			default:
				Fail("unexpected request")
				return nil, nil
			}
		})
		receipt, err := source.CreateTicket(context.Background(), spec)
		Expect(err).NotTo(HaveOccurred())
		Expect(receipt.Issue.HtmlUrl).To(Equal("https://github.com/example/project/issues/3"))
		Expect(receipt.LinkedDependencies).To(HaveLen(2))
		Expect(calls).To(Equal(5))
	}, Entry("canonical spelling", "example/project", "https://github.com/example/project/issues/1"),
		Entry("noncanonical capitalization", "EXample/PROject", "https://github.com/EXample/PROject/issues/1"))

	DescribeTable("enforces the native blocker limit independently of stackable prerequisites", func(strict, stackable int, valid bool) {
		spec := workcontinuity.TicketTemplate()
		spec.Dependencies = nil
		for index := range strict + stackable {
			condition := workv1.DependencyCondition_DEPENDENCY_CONDITION_COMPLETED
			if index >= strict {
				condition = workv1.DependencyCondition_DEPENDENCY_CONDITION_STACKED_OR_MERGED
			}
			spec.Dependencies = append(spec.Dependencies, &workv1.TicketDependency{
				IssueUrl:  fmt.Sprintf("https://github.com/example/project/issues/%d", index+1),
				Condition: condition,
			})
		}
		if valid {
			_, err := workcontinuity.RenderTicket(spec)
			Expect(err).NotTo(HaveOccurred())
			return
		}
		source := workcontinuity.NewGitHubSource(func(ctx context.Context, args ...string) ([]byte, error) {
			Fail("oversized blocker lists must fail before any GitHub call")
			return nil, nil
		})
		receipt, err := source.CreateTicket(context.Background(), spec)
		Expect(err).To(MatchError(ContainSubstring("limit of 50 native blockers")))
		Expect(receipt).To(BeNil())
	}, Entry("50 strict", 50, 0, true), Entry("51 strict", 51, 0, false),
		Entry("50 strict plus stackable", 50, 1, true), Entry("51 stackable", 0, 51, true))

	DescribeTable("does not create after failed dependency resolution", func(response string, failure error) {
		calls := 0
		source := workcontinuity.NewGitHubSource(func(ctx context.Context, args ...string) ([]byte, error) {
			calls++
			Expect(args).To(Equal([]string{"api", "repos/example/project/issues/1"}))
			return []byte(response), failure
		})
		receipt, err := source.CreateTicket(context.Background(), workcontinuity.TicketTemplate())
		Expect(err).To(HaveOccurred())
		Expect(receipt).To(BeNil())
		Expect(calls).To(Equal(1))
	},
		Entry("missing permission", "", fmt.Errorf("forbidden")),
		Entry("malformed response", "not-json", nil),
		Entry("pull request URL", `{"id":101,"number":1,"state":"open","html_url":"https://github.com/example/project/pull/1"}`, nil),
		Entry("missing source ID", `{"number":1,"state":"open","html_url":"https://github.com/example/project/issues/1"}`, nil),
	)

	It("returns the created issue in a partial receipt when dependency attachment fails", func() {
		spec := workcontinuity.TicketTemplate()
		spec.Dependencies[0].Condition = workv1.DependencyCondition_DEPENDENCY_CONDITION_IMPLEMENTATION_MERGED
		body, err := workcontinuity.RenderTicket(spec)
		Expect(err).NotTo(HaveOccurred())
		calls := 0
		source := workcontinuity.NewGitHubSource(func(ctx context.Context, args ...string) ([]byte, error) {
			calls++
			switch calls {
			case 1:
				return []byte(`{"id":101,"number":1,"state":"open","html_url":"https://github.com/example/project/issues/1"}`), nil
			case 2:
				return json.Marshal(map[string]any{"number": 3, "title": spec.Title, "body": body, "html_url": "https://github.com/example/project/issues/3"})
			default:
				return nil, fmt.Errorf("dependency API unavailable")
			}
		})
		receipt, err := source.CreateTicket(context.Background(), spec)
		Expect(err).To(MatchError(ContainSubstring("ticket created at https://github.com/example/project/issues/3")))
		Expect(receipt.Issue.Number).To(Equal(int64(3)))
		Expect(receipt.LinkedDependencies).To(BeEmpty())
		Expect(calls).To(Equal(3))
	})

	DescribeTable("keeps stackable prerequisites out of native blockers", func(includeStrict bool) {
		spec := workcontinuity.TicketTemplate()
		if includeStrict {
			spec.Dependencies = append(spec.Dependencies, &workv1.TicketDependency{
				IssueUrl:  "https://github.com/example/project/issues/2",
				Condition: workv1.DependencyCondition_DEPENDENCY_CONDITION_COMPLETED,
			})
		}
		body, err := workcontinuity.RenderTicket(spec)
		Expect(err).NotTo(HaveOccurred())
		Expect(body).To(ContainSubstring("stack these changes on top"))
		created := false
		linked := false
		source := workcontinuity.NewGitHubSource(func(ctx context.Context, args ...string) ([]byte, error) {
			switch args[1] {
			case "repos/example/project/issues/1":
				Expect(created).To(BeFalse())
				return []byte(`{"id":101,"number":1,"state":"open","html_url":"https://github.com/example/project/issues/1"}`), nil
			case "repos/example/project/issues/2":
				Expect(created).To(BeFalse())
				return []byte(`{"id":102,"number":2,"state":"open","html_url":"https://github.com/example/project/issues/2"}`), nil
			case "repos/example/project/issues":
				created = true
				Expect(args).To(ContainElement("body=" + body))
				return json.Marshal(map[string]any{"number": 3, "title": spec.Title, "body": body, "html_url": "https://github.com/example/project/issues/3"})
			case "repos/example/project/issues/3/dependencies/blocked_by":
				Expect(includeStrict).To(BeTrue(), "a stackable dependency must not create a native blocker")
				Expect(args).To(ContainElement("issue_id=102"))
				Expect(args).NotTo(ContainElement("issue_id=101"))
				linked = true
				return []byte(`{}`), nil
			default:
				Fail("unexpected request")
				return nil, nil
			}
		})
		receipt, err := source.CreateTicket(context.Background(), spec)
		Expect(err).NotTo(HaveOccurred())
		Expect(created).To(BeTrue())
		Expect(linked).To(Equal(includeStrict))
		Expect(receipt.Issue.Body).To(Equal(body))
		if includeStrict {
			Expect(receipt.LinkedDependencies).To(ConsistOf(spec.Dependencies[1]))
		} else {
			Expect(receipt.LinkedDependencies).To(BeEmpty())
		}
	}, Entry("stackable only", false), Entry("mixed with a strict prerequisite", true))
})
