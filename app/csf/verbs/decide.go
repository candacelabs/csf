package verbs

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	ionet "github.com/candacelabs/csf/io/net"
	iohttp "github.com/candacelabs/csf/io/net/http"
	"github.com/candacelabs/csf/io/net/model/jev"
	"github.com/candacelabs/csf/pkg/atomicfile"
)

const (
	decideCommand = "decide"

	decideModelFlag    = "model"
	decideEndpointFlag = "endpoint"
	decideStateFlag    = "state"
	decideJSONFlag     = "json"
	decideLedgerFlag   = "ledger"

	// choiceOptionsSeparator splits a --choice value into its question and
	// options; choiceOptionSeparator splits the options.
	choiceOptionsSeparator = "::"
	choiceOptionSeparator  = "|"

	// decideTimeout bounds one whole decision: the first call loads the model
	// into memory before it answers.
	decideTimeout = 3 * time.Minute
	// maxDecideStateBytes bounds the state read from standard input; the
	// model reads at most 1,024 tokens of it.
	maxDecideStateBytes = 64 * 1024

	// decideLedgerMode is the record's mode: the operator's own, like the
	// other per-service records under the state directory.
	decideLedgerMode = 0o600

	barWidth = 30
	barFull  = "█"
	barEmpty = "·"
)

var errNoDecideQuestions = errors.New("decide needs at least one --noul, --choice or --score question")

// decideRequest is one parsed `csf decide` invocation.
type decideRequest struct {
	model     jev.DeciderModel
	endpoint  string
	state     string
	questions []jev.Question
	json      bool
	// ledger is the decision record to append to; empty records nothing, so a
	// bare decide leaves the machine's state untouched.
	ledger string
}

// decisionQuestionFlag appends one question of its kind to a shared list, so
// the questions keep the order they were given in across the three flags.
type decisionQuestionFlag struct {
	kind      jev.Kind
	questions *[]jev.Question
}

func (question decisionQuestionFlag) String() string { return "" }

func (question decisionQuestionFlag) Set(value string) error {
	if question.kind != jev.KindChoice {
		*question.questions = append(*question.questions, jev.Question{Kind: question.kind, Question: value})
		return nil
	}
	text, options, found := strings.Cut(value, choiceOptionsSeparator)
	if !found {
		return fmt.Errorf("a choice is %q, for example %q", "QUESTION :: A | B | C", "What do they want? :: refund | replacement")
	}
	parsed := []string{}
	for _, option := range strings.Split(options, choiceOptionSeparator) {
		parsed = append(parsed, strings.TrimSpace(option))
	}
	*question.questions = append(*question.questions, jev.Choice(strings.TrimSpace(text), parsed...))
	return nil
}

// decide answers typed questions about a piece of text with a local decider
// model served by Ollama, and prints one probability bar per answer.
func decide(ctx context.Context, arguments []string, input io.Reader, output io.Writer) error {
	request, err := parseDecideRequest(arguments, input)
	if err != nil {
		return err
	}
	client, err := iohttp.NewHTTPClient(ionet.NewHostNetwork(), iohttp.WithClientTimeout(decideTimeout))
	if err != nil {
		return err
	}
	decider, err := jev.NewDecider(client, request.model, jev.WithOllamaEndpoint(request.endpoint))
	if err != nil {
		return err
	}
	distributions, usage, err := decider.DecideWithUsage(ctx, request.state, request.questions)
	if err != nil {
		return err
	}
	if request.ledger != "" {
		// The answer is what the run was paid for: a record that cannot be
		// written is reported and the answer still printed, rather than
		// discarding a decision the model already made.
		if err := recordDecide(request.ledger, request.model, len(request.questions), usage); err != nil {
			fmt.Fprintf(os.Stderr, "warning: decision record: %v\n", err)
		}
	}
	if request.json {
		return renderDistributionsJSON(distributions, output)
	}
	return renderDistributions(distributions, output)
}

// recordDecide appends one run to the decision record, replaced whole, so the
// Workbench's JEV panel follows the day's decisions and tokens.
func recordDecide(path string, model jev.DeciderModel, decisions int, usage jev.Usage) error {
	content, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("read decision record: %w", err)
	}
	ledger := jev.ReadDecideLedger(content)
	ledger = ledger.Record(jev.DecisionRun{
		At:           time.Now().UTC(),
		Model:        model.Name,
		Decisions:    int64(decisions),
		PromptTokens: usage.PromptTokens,
		EvalTokens:   usage.EvalTokens,
	})
	encoded, err := ledger.Encode()
	if err != nil {
		return fmt.Errorf("encode decision record: %w", err)
	}
	if err := atomicfile.WriteFile(path, encoded, decideLedgerMode); err != nil {
		return fmt.Errorf("write decision record: %w", err)
	}
	return nil
}

func parseDecideRequest(arguments []string, input io.Reader) (*decideRequest, error) {
	request := &decideRequest{}
	modelName := ""
	flags := flag.NewFlagSet(decideCommand, flag.ContinueOnError)
	flags.StringVar(&modelName, decideModelFlag, jev.DefaultDeciderModel.Name, "the declared decision model to answer with")
	flags.StringVar(&request.endpoint, decideEndpointFlag, jev.DefaultOllamaEndpoint, "Ollama server URL")
	flags.StringVar(&request.state, decideStateFlag, "", "the text to decide about (default: standard input)")
	flags.BoolVar(&request.json, decideJSONFlag, false, "print the distributions as JSON")
	flags.StringVar(&request.ledger, decideLedgerFlag, "", "append this run to the decision record at PATH (default: record nothing)")
	flags.Var(decisionQuestionFlag{kind: jev.KindNoul, questions: &request.questions}, string(jev.KindNoul), "a statement that is true or false (repeatable)")
	flags.Var(decisionQuestionFlag{kind: jev.KindChoice, questions: &request.questions}, string(jev.KindChoice), "QUESTION :: A | B | C, choosing one of 2-16 options (repeatable)")
	flags.Var(decisionQuestionFlag{kind: jev.KindScore, questions: &request.questions}, string(jev.KindScore), "a question answered on a 0-5 scale (repeatable)")
	if err := flags.Parse(arguments); err != nil {
		return nil, err
	}
	if flags.NArg() != 0 {
		return nil, fmt.Errorf("decide takes flags only; unexpected %q", flags.Arg(0))
	}
	model, err := jev.DeclaredModel(modelName)
	if err != nil {
		return nil, err
	}
	request.model = model
	if len(request.questions) == 0 {
		return nil, errNoDecideQuestions
	}
	if request.state == "" {
		content, err := io.ReadAll(io.LimitReader(input, maxDecideStateBytes+1))
		if err != nil {
			return nil, fmt.Errorf("read state: %w", err)
		}
		if len(content) > maxDecideStateBytes {
			return nil, fmt.Errorf("state exceeds %d bytes", maxDecideStateBytes)
		}
		request.state = string(content)
	}
	return request, nil
}

func renderDistributions(distributions []*jev.Distribution, output io.Writer) error {
	var page strings.Builder
	for index, distribution := range distributions {
		top, _ := distribution.Top()
		fmt.Fprintf(&page, "%d. %s  %s\n", index+1, distribution.Question.Kind, distribution.Question.Question)
		width := 0
		for _, answer := range distribution.Answers {
			width = max(width, len(answer))
		}
		for answerIndex, answer := range distribution.Answers {
			probability := distribution.Probabilities[answerIndex]
			filled := int(probability*barWidth + 0.5)
			marker := ""
			if answer == top {
				marker = "  <"
			}
			fmt.Fprintf(&page, "   %-*s %s%s %5.1f%%%s\n", width, answer,
				strings.Repeat(barFull, filled), strings.Repeat(barEmpty, barWidth-filled), probability*100, marker)
		}
		if distribution.Question.Kind == jev.KindScore {
			fmt.Fprintf(&page, "   expected score %.2f\n", distribution.Expected())
		}
		page.WriteString("\n")
	}
	_, err := io.WriteString(output, page.String())
	return err
}

// decidedAnswer and decidedQuestion are the --json output's shape.
type decidedAnswer struct {
	Answer      string  `json:"answer"`
	Probability float64 `json:"probability"`
}

type decidedQuestion struct {
	Kind     jev.Kind        `json:"kind"`
	Question string          `json:"question"`
	Top      string          `json:"top"`
	Expected *float64        `json:"expected_score,omitempty"`
	Answers  []decidedAnswer `json:"answers"`
}

func renderDistributionsJSON(distributions []*jev.Distribution, output io.Writer) error {
	decided := make([]decidedQuestion, 0, len(distributions))
	for _, distribution := range distributions {
		top, _ := distribution.Top()
		question := decidedQuestion{Kind: distribution.Question.Kind, Question: distribution.Question.Question, Top: top}
		if distribution.Question.Kind == jev.KindScore {
			expected := distribution.Expected()
			question.Expected = &expected
		}
		for index, answer := range distribution.Answers {
			question.Answers = append(question.Answers, decidedAnswer{Answer: answer, Probability: distribution.Probabilities[index]})
		}
		decided = append(decided, question)
	}
	encoder := json.NewEncoder(output)
	encoder.SetIndent("", "  ")
	return encoder.Encode(decided)
}
