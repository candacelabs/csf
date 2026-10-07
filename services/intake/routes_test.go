// Copyright 2026 Candace Labs

package intake_test

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	intakev1 "github.com/candacelabs/csf/proto/candace/intake/v1"
	"github.com/candacelabs/csf/services/intake"
	"github.com/candacelabs/csf/services/relay"
)

const (
	owner    relay.AgentID = "owner"
	fallback relay.AgentID = "fallback"
)

var _ = Describe("StaticRoutes", func() {
	It("parses a route list and prefers a subject's own route over its repository's", func(ctx SpecContext) {
		parsed, err := intake.ParseRoutes(" candacelabs/widgets#7=owner , candacelabs/widgets=fallback,")
		Expect(err).NotTo(HaveOccurred())
		Expect(parsed).To(Equal([]intake.Route{
			{Repository: repository, Number: pullRequest, Agent: owner},
			{Repository: repository, Agent: fallback},
		}))
		routes, err := intake.NewStaticRoutes(parsed...)
		Expect(err).NotTo(HaveOccurred())
		Expect(routes.Repositories()).To(Equal([]string{repository}))

		agent, err := routes.Route(ctx, &intakev1.Subject{Repository: repository, Number: pullRequest})
		Expect(err).NotTo(HaveOccurred())
		Expect(agent).To(Equal(owner))
		agent, err = routes.Route(ctx, &intakev1.Subject{Repository: repository, Number: 8})
		Expect(err).NotTo(HaveOccurred())
		Expect(agent).To(Equal(fallback))
		_, err = routes.Route(ctx, &intakev1.Subject{Repository: "candacelabs/other", Number: pullRequest})
		Expect(err).To(MatchError(intake.ErrUnrouted))
		_, err = routes.Route(ctx, nil)
		Expect(err).To(MatchError(intake.ErrUnrouted))
	})

	It("answers a canceled caller with its context's error", func(ctx SpecContext) {
		routes, err := intake.NewStaticRoutes(intake.Route{Repository: repository, Agent: owner})
		Expect(err).NotTo(HaveOccurred())
		canceled, cancel := context.WithCancel(ctx)
		cancel()
		_, err = routes.Route(canceled, &intakev1.Subject{Repository: repository})
		Expect(err).To(MatchError(context.Canceled))
	})

	DescribeTable("rejects a malformed route list",
		func(specification string) {
			_, err := intake.ParseRoutes(specification)
			Expect(err).To(MatchError(intake.ErrInvalidRoute))
		},
		Entry("empty", " , "),
		Entry("no agent", "candacelabs/widgets"),
		Entry("no number after #", "candacelabs/widgets#=owner"),
		Entry("zero number", "candacelabs/widgets#0=owner"),
	)

	DescribeTable("rejects an invalid table",
		func(routes ...intake.Route) {
			_, err := intake.NewStaticRoutes(routes...)
			Expect(err).To(MatchError(intake.ErrInvalidRoute))
		},
		Entry("a repository that is not owner/name", intake.Route{Repository: "widgets", Agent: owner}),
		Entry("an invalid agent", intake.Route{Repository: repository, Agent: "Owner!"}),
		Entry("one subject routed twice",
			intake.Route{Repository: repository, Number: pullRequest, Agent: owner},
			intake.Route{Repository: repository, Number: pullRequest, Agent: fallback}),
	)
})
