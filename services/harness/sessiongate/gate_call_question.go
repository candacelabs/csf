// Copyright 2026 Candace Labs

package sessiongate

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"regexp"
	"slices"
	"strings"
	"unicode"

	"github.com/candacelabs/csf/pkg/terms"
	"github.com/candacelabs/csf/services/harness/session"
)

// The question gate's rules: what a question to the operator may not be.
const (
	// RuleQuestionClass: a question to the operator is in an operator class
	// and names it, or it is the agent's to resolve and is refused.
	RuleQuestionClass Rule = "question_class"
	// RuleRuling: a question, or an alternative it offers, names something a
	// recorded ruling rules out.
	RuleRuling Rule = "ruling"
)

// QuestionClass is the class a question to the operator falls in: one of the
// three only the operator can answer, or one of the sub-classes the agent
// owes a resolution for.
type QuestionClass string

// The operator classes: meaning, trust boundaries and class membership are the
// operator's to decide (#145).
const (
	ClassMeaning         QuestionClass = "meaning"
	ClassTrustBoundary   QuestionClass = "trust boundary"
	ClassClassMembership QuestionClass = "class membership"
)

// The agent's sub-classes: a question in one is refused with the resolution
// the agent owes instead. ClassRelitigation is a question or offered
// alternative a recorded ruling already answers; ClassUnnamed is a question
// that names no operator class.
const (
	ClassMagnitude    QuestionClass = "magnitude"
	ClassFact         QuestionClass = "fact"
	ClassAuthorized   QuestionClass = "authorized"
	ClassDefault      QuestionClass = "default"
	ClassRelitigation QuestionClass = "re-litigation"
	ClassUnnamed      QuestionClass = "unnamed"
)

// operatorClasses are the classes a question may put to the operator, as its
// tag spells them.
var operatorClasses = []QuestionClass{ClassMeaning, ClassTrustBoundary, ClassClassMembership}

// classTag is how a question names its operator class: "[meaning]",
// "[trust boundary]" or "[class membership]", in the question or its header.
var classTag = regexp.MustCompile(`(?i)\[(meaning|trust[ -]boundary|class[ -]membership)\]`)

// subClassRule is one agent sub-class: the lexicon that places a question in
// it and the resolution the refusal names.
type subClassRule struct {
	class      QuestionClass
	lexicon    *regexp.Regexp
	resolution string
}

// subClassRules are the agent sub-classes in the order a question is tested
// against them; the first match wins. The lexicons are the ticket's (#145).
//
// # Derivation
//
// Replayed over the operator's transcripts (a private corpus, not in this
// repository; measured 2026-10-05): 80 agent turns that ended on a question
// or called AskUserQuestion, 140 questions. Each question was joined to the
// operator's next message: rejected (an exasperation lexicon or mostly
// capitals), answered (an affirmation, or a term the question used) or
// ignored. Per class, n / rejected / answered / ignored: authorized 31 / 4 /
// 5 / 22, magnitude 12 / 0 / 1 / 11, fact 7 / 0 / 0 / 7, default 3 / 0 / 0 /
// 3, unnamed 87 / 22 / 14 / 51. No question in the corpus named an operator
// class, since the tag did not exist, so every one would have been refused;
// the 20 answered are the upper bound on questions the operator wanted (14%).
// Two rules were changed by the replay: a sentence ends only at punctuation
// before whitespace ("Qwen3.8-27B" had been split into "8-27B?"), and a
// number counts as a magnitude only standing alone, not inside a host name
// or an issue reference. The sub-class only chooses the resolution the
// refusal names; the operator's overrides (question_wanted on a send) are
// the live false-positive evidence.
var subClassRules = []subClassRule{
	{
		class:      ClassAuthorized,
		lexicon:    regexp.MustCompile(`(?i)\b(should i|shall i|want me to|would you like me to|should we (go ahead|proceed|start|run|ship|merge|switch)|ok(ay)? to|go ahead|proceed)\b`),
		resolution: "proceed: your assignment already authorizes it; report what you did",
	},
	{
		class:      ClassMagnitude,
		lexicon:    regexp.MustCompile(`(?i)((^|[\s(~$])\d[\d,]*(\.\d+)?(%|[kmgx×]?b?\b)(\s|[?,).]|$)|\b(threshold|level|limit|budget|weight|timeout|cap|cutoff|minimum|maximum|interval|frequency|ratio|quota|ttl|percent(age)?|how (many|much|long|often|big|large|small))s?\b)`),
		resolution: "replace the question with a derivation plan (the metric, its outcome curve, the knee, the recompute trigger), derive the value from data and proceed report-only",
	},
	{
		class:      ClassFact,
		lexicon:    regexp.MustCompile(`(?i)(^\W*(does|do|did|is there|are there)\b|\bwhich (flag|file|command|option|version|port|path|branch|image|binary)\b|\bwhere (is|are|does|do)\b|\bexists?\b)`),
		resolution: "look it up (the code, --help, the API, the event log) and state what you found",
	},
	{
		class:      ClassDefault,
		lexicon:    regexp.MustCompile(`(?i)\b(naming|rename|format(ting)?|ordering|casing|spelling|prefix|suffix|indent(ation)?)\b`),
		resolution: "apply the house rule that covers it and name the rule",
	},
}

// Question is one question an agent put to the operator: its text, the
// header the asking tool gave it, and the alternatives it offered.
type Question struct {
	Text    string
	Header  string
	Options []string
}

// askedQuestions is the input of one AskUserQuestion call.
type askedQuestions struct {
	Questions []struct {
		Question string `json:"question"`
		Header   string `json:"header"`
		Options  []struct {
			Label       string `json:"label"`
			Description string `json:"description"`
		} `json:"options"`
	} `json:"questions"`
}

// AskedQuestions reads the questions of one AskUserQuestion call's input; an
// input of another shape asks nothing.
func AskedQuestions(input json.RawMessage) []Question {
	var asked askedQuestions
	if json.Unmarshal(input, &asked) != nil {
		return []Question{}
	}
	questions := make([]Question, 0, len(asked.Questions))
	for _, one := range asked.Questions {
		question := Question{Text: one.Question, Header: one.Header, Options: []string{}}
		for _, option := range one.Options {
			question.Options = append(question.Options, strings.TrimSpace(option.Label+" "+option.Description))
		}
		questions = append(questions, question)
	}
	return questions
}

// question is the PreToolUse gate on AskUserQuestion: the call's questions
// are judged against the turn's rulings before the operator sees them, and a
// refused one is denied with the resolution the agent owes. A turn log that
// cannot be read is judged against no rulings.
func (call *gateCall) question(ctx context.Context) *HookOutput {
	var hook struct {
		ToolInput json.RawMessage `json:"tool_input"`
	}
	_ = json.Unmarshal(call.input, &hook)
	rulings := []session.Ruling{}
	if records, err := session.ReadTurnRecords(call.gate.directory); err == nil {
		rulings = turnOf(records).rulings
	}
	verdicts, findings := JudgeQuestions(AskedQuestions(hook.ToolInput), rulings)
	if len(findings) == 0 {
		call.record(ctx, GateQuestion, DecisionAllow, slog.Any(KeyQuestions, verdicts))
		return nil
	}
	rules := make([]string, 0, len(findings))
	messages := make([]string, 0, len(findings))
	for _, finding := range findings {
		rules = append(rules, string(finding.Rule))
		messages = append(messages, finding.Message)
	}
	reason := questionRejection + strings.Join(messages, "; ") + "."
	call.record(ctx, GateQuestion, DecisionDeny, slog.Any(keyRules, rules), slog.String(keyReason, reason), slog.Any(KeyQuestions, verdicts))
	return &HookOutput{HookSpecificOutput: &HookSpecificOutput{
		HookEventName:            session.HookPreToolUse,
		PermissionDecision:       permissionDeny,
		PermissionDecisionReason: reason,
	}}
}

const questionRejection = "Rejected by the CSF question gate. "

// closingMarkup is the Markdown that may close a sentence after its question
// mark: emphasis and a closing parenthesis.
const closingMarkup = "*_)"

// paragraphBreak separates a reply's paragraphs.
var paragraphBreak = regexp.MustCompile(`\n[ \t]*\n`)

// listItem is a line offering an alternative: a bullet, a number or a letter.
var listItem = regexp.MustCompile(`^\s*(?:[-*+]|\d+[.)]|[A-Za-z][.)])\s+(.+)$`)

// sentenceBreak ends a sentence: terminal punctuation before whitespace, so
// "3.8" and "e.g.," stay inside theirs.
var sentenceBreak = regexp.MustCompile(`[.!?]+\s+`)

// ReplyQuestions reads the questions a reply ends on: when the reply's last
// prose ends in a question mark, every question sentence of its last
// paragraph, each offering the list items of that paragraph and of the
// paragraph before it. A reply that ends otherwise asks the operator nothing.
// Code is not prose.
func ReplyQuestions(reply string) []Question {
	prose := strings.TrimSpace(terms.StripCode(reply))
	if !strings.HasSuffix(strings.TrimRight(prose, closingMarkup), "?") {
		return []Question{}
	}
	paragraphs := paragraphBreak.Split(prose, -1)
	last := paragraphs[len(paragraphs)-1]
	options := listItems(last)
	if len(paragraphs) > 1 {
		options = append(listItems(paragraphs[len(paragraphs)-2]), options...)
	}
	questions := []Question{}
	for _, line := range strings.Split(last, "\n") {
		for _, sentence := range sentences(line) {
			if strings.HasSuffix(strings.TrimRight(sentence, closingMarkup), "?") {
				questions = append(questions, Question{Text: sentence, Options: options})
			}
		}
	}
	return questions
}

// sentences splits one line into its sentences, each with its terminal
// punctuation.
func sentences(line string) []string {
	split := []string{}
	start := 0
	for _, span := range sentenceBreak.FindAllStringIndex(line, -1) {
		split = append(split, strings.TrimSpace(line[start:span[1]]))
		start = span[1]
	}
	if rest := strings.TrimSpace(line[start:]); rest != "" {
		split = append(split, rest)
	}
	return split
}

func listItems(paragraph string) []string {
	items := []string{}
	for _, line := range strings.Split(paragraph, "\n") {
		if match := listItem.FindStringSubmatch(line); match != nil && !strings.HasSuffix(strings.TrimSpace(match[1]), "?") {
			items = append(items, strings.TrimSpace(match[1]))
		}
	}
	return items
}

// ClassifyQuestion places one question: the first agent sub-class whose
// lexicon matches it, else the operator class it names, else unnamed.
func ClassifyQuestion(question Question) QuestionClass {
	for _, rule := range subClassRules {
		if rule.lexicon.MatchString(question.Text) {
			return rule.class
		}
	}
	tag := classTag.FindStringSubmatch(question.Text + " " + question.Header)
	if tag == nil {
		return ClassUnnamed
	}
	named := QuestionClass(strings.ReplaceAll(strings.ToLower(tag[1]), "-", " "))
	if slices.Contains(operatorClasses, named) {
		return named
	}
	return ClassUnnamed
}

// JudgeQuestions applies the question gate's rules to the questions of one
// reply or one AskUserQuestion call against the rulings in force: each
// question's verdict, in order, and the findings that refuse them.
func JudgeQuestions(questions []Question, rulings []session.Ruling) ([]session.QuestionVerdict, []ReplyFinding) {
	verdicts := make([]session.QuestionVerdict, 0, len(questions))
	findings := []ReplyFinding{}
	for _, question := range questions {
		if contradicted := contradictions(question, rulings); len(contradicted) > 0 {
			verdicts = append(verdicts, session.QuestionVerdict{Class: string(ClassRelitigation), Blocked: true})
			findings = append(findings, ReplyFinding{Rule: RuleRuling, Message: rulingMessage(question, contradicted)})
			continue
		}
		class := ClassifyQuestion(question)
		if slices.Contains(operatorClasses, class) {
			verdicts = append(verdicts, session.QuestionVerdict{Class: string(class)})
			continue
		}
		verdicts = append(verdicts, session.QuestionVerdict{Class: string(class), Blocked: true})
		findings = append(findings, ReplyFinding{Rule: RuleQuestionClass, Message: questionClassMessage(question, class)})
	}
	return verdicts, findings
}

// contradiction is one alternative a ruling rules out, found in a question.
type contradiction struct {
	ruling   session.Ruling
	excluded string
	offered  string
}

// contradictions are the alternatives the question offers, or the question
// itself, naming something a ruling excludes.
func contradictions(question Question, rulings []session.Ruling) []contradiction {
	found := []contradiction{}
	for _, offered := range append([]string{question.Text}, question.Options...) {
		words := stemmedWords(offered)
		for _, ruling := range rulings {
			for _, excluded := range ruling.Excludes {
				if containsRun(words, stemmedWords(excluded)) {
					found = append(found, contradiction{ruling: ruling, excluded: excluded, offered: offered})
				}
			}
		}
	}
	return found
}

// stemmedWords are the words of text, lowercased and stemmed, in order.
func stemmedWords(text string) []string {
	fields := strings.FieldsFunc(strings.ToLower(text), func(character rune) bool {
		return !unicode.IsLetter(character) && !unicode.IsDigit(character)
	})
	stems := make([]string, 0, len(fields))
	for _, field := range fields {
		stems = append(stems, terms.Stem(field))
	}
	return stems
}

// containsRun reports whether run occurs in words as consecutive words.
func containsRun(words []string, run []string) bool {
	if len(run) == 0 {
		return false
	}
	for start := 0; start+len(run) <= len(words); start++ {
		if slices.Equal(words[start:start+len(run)], run) {
			return true
		}
	}
	return false
}

func rulingMessage(question Question, contradicted []contradiction) string {
	parts := make([]string, 0, len(contradicted))
	for _, found := range contradicted {
		parts = append(parts, fmt.Sprintf("%q names %q, which ruling %s rules out (%s)",
			quotation(found.offered), found.excluded, found.ruling.ID, found.ruling.Statement))
	}
	return fmt.Sprintf("%s: in %q, %s. Drop every alternative a ruling rules out; when one alternative is left, drop the question and proceed with it",
		RuleRuling, quotation(question.Text), strings.Join(parts, "; "))
}

func questionClassMessage(question Question, class QuestionClass) string {
	if class == ClassUnnamed {
		return fmt.Sprintf("%s: %q names no operator class. Only meaning, trust boundaries and class membership are the operator's to decide; "+
			"tag such a question [meaning], [trust boundary] or [class membership], and resolve any other yourself",
			RuleQuestionClass, quotation(question.Text))
	}
	resolution := ""
	for _, rule := range subClassRules {
		if rule.class == class {
			resolution = rule.resolution
		}
	}
	return fmt.Sprintf("%s: %q falls in the %s sub-class, which is not the operator's to answer: %s", RuleQuestionClass, quotation(question.Text), class, resolution)
}
