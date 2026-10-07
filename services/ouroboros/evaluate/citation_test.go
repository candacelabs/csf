// Copyright 2026 Candace Labs

package evaluate_test

import (
	"context"
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/services/ouroboros/evaluate"
)

var _ = Describe("The proof check", func() {
	const live, candidate = "aaaaaaaaaaaa", "bbbbbbbbbbbb"
	suite := evaluate.Suite{Version: 2, Tickets: []evaluate.Ticket{ticket(1, 100, 1), ticket(2, 100, 1)}}
	liveScore := evaluate.ScoreOf(live, suite, []evaluate.Replay{replay(live, 2, 1, 100, 4), replay(live, 2, 2, 100, 4)}, 0)
	candidateScore := evaluate.ScoreOf(candidate, suite, []evaluate.Replay{replay(candidate, 2, 1, 100, 2), replay(candidate, 2, 2, 100, 2)}, 0)
	// recorded gives the line the records hold for exactly the pair above.
	recorded := func(_ context.Context, build string, version int, versus string) (string, error) {
		if build != candidate || version != 2 || versus != live {
			return "", evaluate.ErrUnscored
		}
		return evaluate.Citation(candidateScore, liveScore)
	}

	It("writes a citation line it reads back", func() {
		line, err := evaluate.Citation(candidateScore, liveScore)
		Expect(err).NotTo(HaveOccurred())
		Expect(line).To(Equal("eval-suite: build=bbbbbbbbbbbb suite=v2 score=20.0 [5.4, 51.2] delta=-20.0 vs=aaaaaaaaaaaa"))
		Expect(evaluate.Citations("text\n" + line + "\nmore")).To(Equal([]evaluate.Cited{{Build: candidate, Version: 2, Versus: live, Line: line}}))
	})

	It("passes a body that claims no improvement", func() {
		body := "**Verdict:** Inconclusive: the wiring is in, unscored.\n**Before:** fewer people knew.\n"
		Expect(evaluate.CheckClaims(context.Background(), body, recorded)).To(Succeed())
	})

	It("refuses an improvement claimed without a citation", func() {
		body := "**Verdict:** Good: replays struggle less.\n**Now:** 30% fewer struggle episodes.\n"
		Expect(evaluate.CheckClaims(context.Background(), body, recorded)).To(MatchError(evaluate.ErrUncited))
	})

	It("refuses a citation whose numbers the records do not give", func() {
		body := "**Verdict:** Good: an improvement.\neval-suite: build=bbbbbbbbbbbb suite=v2 score=9.0 [3.6, 21.8] delta=-11.0 vs=aaaaaaaaaaaa\n"
		err := evaluate.CheckClaims(context.Background(), body, recorded)
		Expect(err).To(MatchError(evaluate.ErrUncited))
		Expect(err.Error()).To(ContainSubstring("does not match the records"))
	})

	It("refuses a citation of a build the records have not scored", func() {
		body := "**Now:** faster.\neval-suite: build=cccccccccccc suite=v2 score=10.0 [3.6, 21.8] delta=-10.0 vs=aaaaaaaaaaaa\n"
		Expect(evaluate.CheckClaims(context.Background(), body, recorded)).To(MatchError(evaluate.ErrUncited))
	})

	It("passes an improvement that cites the recorded line", func() {
		line, err := evaluate.Citation(candidateScore, liveScore)
		Expect(err).NotTo(HaveOccurred())
		body := "**Verdict:** Good: replays struggle less.\n\n" + line + "\n"
		Expect(evaluate.CheckClaims(context.Background(), body, recorded)).To(Succeed())
	})

	It("cannot cite a score with no tool call", func() {
		_, err := evaluate.Citation(evaluate.Score{Build: candidate}, liveScore)
		Expect(errors.Is(err, evaluate.ErrNoRate)).To(BeTrue())
	})
})
