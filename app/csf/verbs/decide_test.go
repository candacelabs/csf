package verbs

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/io/net/model/jev"
)

var _ = Describe("csf decide", func() {
	It("keeps the questions in the order given across the three flags", func() {
		request, err := parseDecideRequest([]string{
			"--state", "My order arrived cracked. I want a refund.",
			"--choice", "What does the customer want? :: refund | replacement | discount",
			"--noul", "The customer is angry.",
			"--score", "How urgent is this ticket?",
		}, strings.NewReader("unused"))
		Expect(err).NotTo(HaveOccurred())
		Expect(request.model).To(Equal(jev.DefaultDeciderModel))
		Expect(request.endpoint).To(Equal(jev.DefaultOllamaEndpoint))
		Expect(request.state).To(Equal("My order arrived cracked. I want a refund."))
		Expect(request.questions).To(Equal([]jev.Question{
			jev.Choice("What does the customer want?", "refund", "replacement", "discount"),
			jev.Noul("The customer is angry."),
			jev.Score("How urgent is this ticket?"),
		}))
	})

	It("reads the state from standard input when --state is absent", func() {
		request, err := parseDecideRequest([]string{"--noul", "It is a bug report."}, strings.NewReader("panic: nil map\n"))
		Expect(err).NotTo(HaveOccurred())
		Expect(request.state).To(Equal("panic: nil map\n"))
	})

	It("refuses no questions, positional arguments and a choice without options", func() {
		_, err := parseDecideRequest([]string{"--state", "text"}, strings.NewReader(""))
		Expect(err).To(MatchError(errNoDecideQuestions))
		_, err = parseDecideRequest([]string{"--noul", "x", "stray"}, strings.NewReader(""))
		Expect(err).To(MatchError(ContainSubstring(`unexpected "stray"`)))
		_, err = parseDecideRequest([]string{"--choice", "Which one?"}, strings.NewReader(""))
		Expect(err).To(MatchError(ContainSubstring("QUESTION :: A | B | C")))
	})

	It("selects a declared model by name and refuses an undeclared one", func() {
		request, err := parseDecideRequest([]string{"--model", "jev_omni_12b_q4", "--noul", "It is a bug report."}, strings.NewReader(""))
		Expect(err).NotTo(HaveOccurred())
		Expect(request.model.Name).To(Equal("jev_omni_12b_q4"))
		_, err = parseDecideRequest([]string{"--model", "unheard_of", "--noul", "x"}, strings.NewReader(""))
		Expect(err).To(MatchError(ContainSubstring("unheard_of")))
	})

	Describe("rendering", func() {
		score := jev.Score("How urgent is this ticket?")
		choice := jev.Choice("What does the customer want?", "refund", "replacement")
		distributions := []*jev.Distribution{
			{Question: choice, Answers: choice.Answers(), Probabilities: []float64{0.9993653893470764, 0.0006339401006698608}},
			{Question: score, Answers: score.Answers(), Probabilities: []float64{0.0028352702502161264, 0.01707780547440052, 0.0933370515704155, 0.4675634503364563, 0.41072896122932434, 0.008457389660179615}},
		}

		It("draws one bar per answer, marks the top answer and gives the expected score", func() {
			var output bytes.Buffer
			Expect(renderDistributions(distributions, &output)).To(Succeed())
			lines := strings.Split(output.String(), "\n")
			Expect(lines[0]).To(Equal("1. choice  What does the customer want?"))
			Expect(lines[1]).To(HavePrefix("   refund      " + strings.Repeat("█", 30)))
			Expect(lines[1]).To(HaveSuffix(" 99.9%  <"))
			Expect(lines[2]).To(HavePrefix("   replacement " + strings.Repeat("·", 30)))
			Expect(lines[2]).To(HaveSuffix("  0.1%"))
			Expect(output.String()).To(ContainSubstring("   3 " + strings.Repeat("█", 14)))
			Expect(output.String()).To(ContainSubstring("expected score 3.29\n"))
		})

		It("prints the same distributions as JSON", func() {
			var output bytes.Buffer
			Expect(renderDistributionsJSON(distributions, &output)).To(Succeed())
			var decided []decidedQuestion
			Expect(json.Unmarshal(output.Bytes(), &decided)).To(Succeed())
			Expect(decided).To(HaveLen(2))
			Expect(decided[0].Top).To(Equal("refund"))
			Expect(decided[0].Expected).To(BeNil())
			Expect(decided[1].Top).To(Equal("3"))
			Expect(*decided[1].Expected).To(BeNumerically("~", 3.2916, 0.0001))
			Expect(decided[1].Answers).To(HaveLen(6))
		})
	})

	Describe("an interrupted run", func() {
		It("asks the model nothing once the context it was given is cancelled", func() {
			var requests atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				requests.Add(1)
			}))
			DeferCleanup(server.Close)
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			err := decide(ctx, []string{"--endpoint", server.URL, "--state", "panic: nil map", "--noul", "It is a bug report."}, strings.NewReader(""), io.Discard)
			Expect(err).To(MatchError(context.Canceled))
			Expect(requests.Load()).To(BeZero())
		})
	})

	Describe("recording the run", func() {
		It("appends one run to the record, keeps the earlier ones and writes it operator-only", func() {
			path := filepath.Join(GinkgoT().TempDir(), jev.DecideLedgerFile)
			model := jev.DeciderModel{Name: "jevk5_9b_q4"}
			Expect(recordDecide(path, model, 3, jev.Usage{PromptTokens: 700, EvalTokens: 115})).To(Succeed())
			Expect(recordDecide(path, model, 2, jev.Usage{PromptTokens: 12, EvalTokens: 1})).To(Succeed())

			content, err := os.ReadFile(path)
			Expect(err).NotTo(HaveOccurred())
			ledger := jev.ReadDecideLedger(content)
			Expect(ledger.Runs).To(HaveLen(2), "the earlier run is kept")
			Expect(ledger.Runs[0].Model).To(Equal("jevk5_9b_q4"))
			Expect(ledger.Runs[0].Decisions).To(Equal(int64(3)))
			Expect(ledger.Runs[0].Tokens()).To(Equal(int64(815)))
			Expect(ledger.Runs[1].Decisions).To(Equal(int64(2)))

			info, err := os.Stat(path)
			Expect(err).NotTo(HaveOccurred())
			Expect(info.Mode().Perm()).To(Equal(os.FileMode(0o600)))
		})

		It("reports an error when the record's directory is absent", func() {
			path := filepath.Join(GinkgoT().TempDir(), "absent", jev.DecideLedgerFile)
			err := recordDecide(path, jev.DeciderModel{Name: "jevk5_9b_q4"}, 1, jev.Usage{PromptTokens: 1})
			Expect(err).To(MatchError(ContainSubstring("write decision record")))
		})
	})
})
