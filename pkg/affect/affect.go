// Copyright 2026 Candace Labs

// Package affect reads an operator's message for operator affect: whether it
// flags an agent's mistake (an operator correction), how strained the operator
// is, whether the operator says so, and whether the message is a directive, a
// question or vision. It is a deterministic proxy, lexical and structural, with
// no model: a pure library that starts no goroutines and crosses no boundary.
//
// # Derivation
//
// Every rule was fit on the operator's history: 962 operator messages that
// answer an agent output, 2026-08-01 to 2026-10-05, a private corpus that is
// not in this repository. Two windows were hand-labelled for "this message
// flags a mistake the agent made": 61 messages of 2026-10-03 to fit the rules
// (24 corrections), and the 57 messages of the orchestrator session of
// 2026-10-04 03:00Z to 2026-10-05 02:05Z held out (23 corrections). What each
// rule measured:
//
//   - A correction is an insult or a quality verdict ("idiot", "sucks",
//     "cringe", "unprofessional", "incomplete"), a mining flag ("mineable
//     offense"), a reproach opener ("why did", "how come", "what're you"),
//     which may follow fillers ("uhhh", "dude", "also"), or a negation opener
//     ("no", "none", "stop"). Shouting was tried as a fifth marker and dropped:
//     on the fit window it added no correction and nine directives shouted for
//     urgency (precision 0.69 against 0.95). On the fit window the rules read
//     precision 19/20 and recall 19/24; on the held-out window precision 17/20
//     = 0.85 and recall 17/23 = 0.74, F1 0.79. The misses are critiques with
//     no marker ("you launched three sessions without follow-up questions").
//   - Shouting is at least [ShoutedCapsRatio] of a message's letters in
//     capitals, over at least [ShoutedMinimumLetters] letters. The capital
//     share of messages is bimodal: 755 of 939 messages of eight letters or
//     more sit under 0.1 and 123 at 0.9 or over, with 3 to 8 per tenth in
//     between, so any cut from 0.3 to 0.8 moves under 2% of messages; 0.5 is
//     the middle of the valley. 15.0% of messages shout.
//   - Strain is high when the message shouts, insults the agent, or the
//     operator reports strain in it. Profanity alone is register, not strain:
//     30.6% of messages carry profanity or an insult, while strain reads high
//     on 20.2%.
//   - Backtest against self-reports: the corpus holds one self-report of
//     strain (2026-10-04), and the self-report marker reads it and no other
//     message. No proxy anticipated it: the ten messages before
//     it shout 0 times (base rate 15.0%) and carry an insult or profanity at
//     30% (base 30.6%). Strain read from the text is therefore a label of the
//     message, not a forecast; [Reading.SelfReport] is the ground truth the
//     next fit is backtested against, and a small classifier is warranted only
//     if self-reports accumulate that the proxy misses.
//   - Vision is a stated non-ask ("no action needed", "just laying out",
//     "long term context", "eventually"): 8 of 962 messages. A vision message
//     is never a correction, even when it opens with "no".
//
// Two consumer bounds were measured on the same corpus, pairing each operator
// message with the agent reply it answers (1,361 pairs):
//
//   - [ReplyWordLimit]: replies under 800 words are followed by a correction
//     12% to 19% of the time in every length band; over 800 words, 25%, and by
//     a strained message 38% against 16% to 28% below it.
//   - Replies that ask the operator a question are followed by fewer
//     corrections (7%, n=128) than replies that do not (18%), so no reply rule
//     forbids a question.
//
// Measured 2026-10-05; the corpus, the labels and the replay stay outside the
// repository because they are the operator's words.
package affect

import (
	"regexp"
	"strings"

	"github.com/candacelabs/csf/pkg/terms"
)

// Strain is how strained the operator reads in one message.
type Strain string

// The strains a reading takes.
const (
	StrainLow  Strain = "low"
	StrainHigh Strain = "high"
)

// Kind is what the message asks of the agent.
type Kind string

// The kinds a reading takes.
const (
	// KindDirective asks the agent to do something; it is every message that
	// is neither a question nor vision.
	KindDirective Kind = "directive"
	// KindQuestion asks the agent to answer.
	KindQuestion Kind = "question"
	// KindVision states where things are going and asks for nothing now.
	KindVision Kind = "vision"
)

// The measured bounds; the package documentation derives each.
const (
	// ShoutedCapsRatio is the least share of a message's letters in capitals
	// that reads as shouting.
	ShoutedCapsRatio = 0.5
	// ShoutedMinimumLetters is the fewest letters a message needs to shout:
	// "OK" is not shouting.
	ShoutedMinimumLetters = 8
	// ReplyWordLimit is the longest reply, in words, that a strained
	// operator's message is answered with.
	ReplyWordLimit = 800
)

// fillers are the openers a reproach may follow.
const fillers = `(?:(?:uh+|um+|gah|dude|bro|bud|ok|okay|wait|also|like|so|and|but|what|yo)\W+)*`

var (
	urls       = regexp.MustCompile(`https?://\S+`)
	profanity  = regexp.MustCompile(`(?i)\b(fuck\w*|shit\w*|bullshit|damn|ass|asshole|bitch|crap|cock|balls|wtf|lmfao|motherfuck\w*|hell)\b`)
	insults    = regexp.MustCompile(`(?i)\b(idiot\w*|moron\w*|dumb\w*|stupid|troglodyte|dickhead|retard\w*|imbecile|clown|cringe|amateur\w*|unprofessional|ugly|sucks?|garbage|trash|dogshit|terrible|awful|bad|broken|incomplete)\b`)
	urgency    = regexp.MustCompile(`(?i)\b(asap|now|immediately|urgent|number one priority|hurry)\b`)
	miningFlag = regexp.MustCompile(`(?i)\b(mine?able|minable|mining signal|mine flags?|offense)\b`)
	reproach   = regexp.MustCompile(`(?i)^\W*` + fillers + `(why (are|did|do|is|isn'?t|the|tf|teh|would|am|can'?t|couldn'?t)|how (come|did|can|could) you|how come|what'?s (fucking )?wrong|what (the|teh) fuck|wtf|where'?s|what'?re you|oh my)`)
	negation   = regexp.MustCompile(`(?i)^\W*(no|nope|none|not|stop|wrong)\b`)
	vision     = regexp.MustCompile(`(?i)(no action needed|just laying out|laying out the vision|long term context|nothing to do|just letting you know|big picture|eventually)`)
	selfReport = regexp.MustCompile(`(?i)\b(go(ing|in) through it|i'?m (so )?(tired|exhausted|burn(ed|t) out|stressed|overwhelmed|frustrated|losing it)|emotionally|mental health|need a break)\b`)
	question   = regexp.MustCompile(`(?i)^\W*` + fillers + `(who|what|when|where|why|how|which|can|could|would|should|is|are|does|did|am)\b`)
)

// Features are the measurements a reading is made of.
type Features struct {
	// CapsRatio is the share of the message's ASCII letters in capitals.
	CapsRatio float64 `json:"caps_ratio"`
	Letters   int     `json:"letters"`
	Shouted   bool    `json:"shouted"`
	Profanity int     `json:"profanity"`
	Insults   int     `json:"insults"`
	Urgency   int     `json:"urgency"`
	// MiningFlag is the operator naming the agent's turn a mining offense.
	MiningFlag bool `json:"mining_flag"`
	Reproach   bool `json:"reproach"`
	Negation   bool `json:"negation"`
}

// Reading is one message's operator affect.
type Reading struct {
	Strain Strain `json:"strain"`
	Kind   Kind   `json:"kind"`
	// Correction is the operator flagging a mistake the agent made.
	Correction bool `json:"correction"`
	// SelfReport is the operator saying, in words, that they are strained.
	SelfReport bool     `json:"self_report"`
	Features   Features `json:"features"`
}

// Dissatisfied reports the operator's dissatisfaction with what they are
// answering: a correction, or strain.
func (reading Reading) Dissatisfied() bool {
	return reading.Correction || reading.Strain == StrainHigh
}

// Read reads one operator message. Code and links are data, not the
// operator's words, and are set aside first.
func Read(message string) Reading {
	prose := strings.TrimSpace(urls.ReplaceAllString(terms.StripCode(message), " "))
	features := Features{
		Profanity:  len(profanity.FindAllString(prose, -1)),
		Insults:    len(insults.FindAllString(prose, -1)),
		Urgency:    len(urgency.FindAllString(prose, -1)),
		MiningFlag: miningFlag.MatchString(prose),
		Reproach:   reproach.MatchString(prose),
		Negation:   negation.MatchString(prose),
	}
	features.CapsRatio, features.Letters = capsRatio(prose)
	features.Shouted = features.CapsRatio >= ShoutedCapsRatio && features.Letters >= ShoutedMinimumLetters
	reading := Reading{Strain: StrainLow, Kind: kindOf(prose), SelfReport: selfReport.MatchString(prose), Features: features}
	if features.Shouted || features.Insults > 0 || reading.SelfReport {
		reading.Strain = StrainHigh
	}
	reading.Correction = reading.Kind != KindVision &&
		(features.Insults > 0 || features.MiningFlag || features.Reproach || features.Negation)
	return reading
}

func kindOf(prose string) Kind {
	switch {
	case vision.MatchString(prose):
		return KindVision
	case strings.HasSuffix(prose, "?") || question.MatchString(prose):
		return KindQuestion
	default:
		return KindDirective
	}
}

// capsRatio is the share of capitals among the ASCII letters of text, and
// how many letters it read.
func capsRatio(text string) (float64, int) {
	letters, capitals := 0, 0
	for _, character := range text {
		switch {
		case character >= 'A' && character <= 'Z':
			capitals++
			letters++
		case character >= 'a' && character <= 'z':
			letters++
		}
	}
	if letters == 0 {
		return 0, 0
	}
	return float64(capitals) / float64(letters), letters
}
