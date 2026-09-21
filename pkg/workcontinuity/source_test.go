package workcontinuity_test

import (
	"context"
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/pkg/workcontinuity"
)

var _ = Describe("GitHub source boundary", func() {
	It("reads all comment pages through the existing CLI and generated projection", func() {
		calls := 0
		source := workcontinuity.NewGitHubSource(func(ctx context.Context, args ...string) ([]byte, error) {
			calls++
			if calls == 1 {
				Expect(args).To(Equal([]string{"api", "repos/example/project/issues/1"}))
				return []byte(fmt.Sprintf(`{"number":1,"html_url":%q,"title":"Task","body":"scope","state":"open","labels":[]}`, taskURL)), nil
			}
			Expect(args).To(Equal([]string{"api", "repos/example/project/issues/1/comments?per_page=100", "--paginate"}))
			return []byte("[{\"id\":1,\"body\":\"first\",\"user\":{\"login\":\"maintainer\",\"id\":42},\"author_association\":\"OWNER\"}]\n[{\"id\":2,\"body\":\"second\",\"author_association\":\"NONE\"}]"), nil
		})
		snapshot, err := source.Load(context.Background(), taskURL)
		Expect(err).NotTo(HaveOccurred())
		Expect(snapshot.Comments).To(HaveLen(2))
		Expect(snapshot.Comments[0].User.Login).To(Equal("maintainer"))
		Expect(snapshot.Comments[0].AuthorAssociation).To(Equal("OWNER"))
		Expect(snapshot.Comments[1].AuthorAssociation).To(Equal("NONE"))
		Expect(calls).To(Equal(2))
	})
	DescribeTable("rejects invalid task destinations without launching anything", func(task string) {
		source := workcontinuity.NewGitHubSource(func(ctx context.Context, args ...string) ([]byte, error) { Fail("must not launch"); return nil, nil })
		_, err := source.Load(context.Background(), task)
		Expect(err).To(HaveOccurred())
	}, Entry("other host", "https://example.invalid/org/repo/issues/1"), Entry("query", taskURL+"?x=1"), Entry("credentials", "https://user@github.com/example/project/issues/1"), Entry("shell", "$(touch /tmp/not-executed)"))
})
