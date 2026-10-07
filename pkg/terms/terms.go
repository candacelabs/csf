// Copyright 2026 Candace Labs

// Package terms reads the terms of a text and tells which of them a
// vocabulary has not seen. It is the extraction behind the harness's unvetted
// operator terms: the words of an operator's message that appear in none of
// the operator's earlier messages. It is a pure library: it starts no
// goroutines and crosses no boundary.
//
// A term is a word of the text after everything that is not vocabulary is
// set aside: fenced and inline code, words carrying digits or identifier
// punctuation (paths, flags, hashes, dotted and underscored names), camelCase
// identifiers, words shorter than [MinimumLength], and common English (the
// stoplist). Words are lowercased, runs of three or more of one letter are
// collapsed to one, and inflections are folded by [Stem], so "cgroups" and
// "cgroup" are one term. The stoplist is matched on the word and on its stem.
//
// # Derivation
//
// Every parameter was fit on the operator's history: 1,903 operator messages
// over 157 sessions, 2026-08-01 to 2026-10-03, a private corpus that is not in
// this repository. The corpus was walked in time order; the first 1,522
// messages built the vocabulary and the last 381 were the holdout whose novel
// terms were hand-labelled as a term worth checking, a borderline domain use
// of an English word, a typo, or an ordinary English word. The rules and what
// each measured on the holdout:
//
//   - Minimum length 3 (2 and 4 were tried): 2 admits letter fragments of
//     identifiers; 4 drops ros, gin, mcp, ssh, bpf and the like.
//   - Identifier words dropped whole before splitting: UUIDs and hashes were a
//     tenth of the novel words when their letter runs were read as words.
//   - Hyphenated words split: whole, the recipe slugs and ad hoc compounds
//     were a third of the novel words (673 against 455 terms).
//   - Stoplist cutoff: the whole frequency list. Flagged terms fell
//     monotonically with the cutoff (320 at none, 287 at 3,000, 258 at 5,000,
//     225 at 10,000) and the words the last step absorbed were common English
//     (census, formula, hypothesis, tonight), none a term.
//   - Stemming: Porter on the text and the stoplist. Against a light
//     plural-and-tense fold it took the flagged share of messages from 33% to
//     28% and the false-positive rate from 69% to 62%, losing four domain uses
//     of English words (compaction, contrastive, coupling, responder).
//   - Rejected: folding a novel word onto a vetted one at edit distance one
//     absorbed real terms (cgroup into group, dockerd into docker, premise
//     into precise: 14 of its 50 absorptions). Folding transpositions alone
//     absorbed cgroup into an earlier typo of it. Stripping prefixes (un-,
//     re-, over-, sub-) saved 3 points and lost subtree, dequeue, unexported,
//     oversize and overfile.
//
// On the holdout the chosen rules, as this package implements them, flag 107
// of 381 operator messages (28.1%), 191 distinct terms, a median of 1 and at
// most 7 per flagged message; by the hand labels 62 are terms, 9 borderline,
// 42 typos and 78 ordinary English, a false-positive rate of 120/191 = 62.8%
// (95% Wilson interval 55.8% to 69.4%). The typos are the operator's and are
// left to the reply: a research check that says "a typo of literally" costs a
// line. The ordinary words are English the frequency list does not reach; a
// larger list would absorb them and the borderline class with them. Measured
// 2026-10-04; the corpus, the labels and the replay program stay outside the
// repository because they are the operator's words.
package terms

import (
	_ "embed"
	"regexp"
	"strings"
	"unicode"
)

// MinimumLength is the shortest word read as a term, in letters.
const MinimumLength = 3

// The text that is never vocabulary.
const (
	stoplistComment = "#"
	// identifierCharacters mark a word as an identifier rather than prose: a
	// digit, a path, a handle, a dotted or underscored name, a flag value, a
	// hash or a home directory.
	identifierCharacters = "0123456789/@\\.=:_#$%~"
	// wordPunctuation is trimmed from both ends of a whitespace-delimited
	// word before it is read.
	wordPunctuation = ".,;:!?()[]{}\"'“”‘’*<>|"
	// repeatLimit is the longest run of one letter kept: "fineeeee" is "fine".
	repeatLimit = 2
)

var (
	fencedCode = regexp.MustCompile("(?s)```.*?```")
	inlineCode = regexp.MustCompile("`[^`\n]*`")
)

//go:embed stoplist.txt
var stoplistText string

// stoplist holds the common words and their stems.
var stoplist = loadStoplist(stoplistText)

// Term is one term of a text as the text spelled it, lowercased.
type Term string

// Vocabulary is the set of terms seen so far, keyed by stem.
type Vocabulary struct {
	stems map[string]struct{}
}

// NewVocabulary builds an empty vocabulary.
func NewVocabulary() *Vocabulary {
	return &Vocabulary{stems: map[string]struct{}{}}
}

// Add records terms as seen.
func (vocabulary *Vocabulary) Add(terms ...Term) {
	for _, term := range terms {
		vocabulary.stems[stemOf(term)] = struct{}{}
	}
}

// Has reports whether a term with the same stem was added.
func (vocabulary *Vocabulary) Has(term Term) bool {
	_, seen := vocabulary.stems[stemOf(term)]
	return seen
}

// Len is the number of distinct stems added.
func (vocabulary *Vocabulary) Len() int { return len(vocabulary.stems) }

// Novel returns the terms of text the vocabulary has not seen, in order of
// first appearance, one per stem. It does not add them.
func (vocabulary *Vocabulary) Novel(text string) []Term {
	novel := []Term{}
	for _, term := range Extract(text) {
		if !vocabulary.Has(term) {
			novel = append(novel, term)
		}
	}
	return novel
}

// StripCode replaces the fenced and inline code in text with spaces: code is
// data, not prose, and never vocabulary.
func StripCode(text string) string {
	return inlineCode.ReplaceAllString(fencedCode.ReplaceAllString(text, " "), " ")
}

// Extract returns the terms of text in order of first appearance, one per
// stem: the first spelling of each is kept.
func Extract(text string) []Term {
	text = StripCode(text)
	seen := map[string]struct{}{}
	terms := []Term{}
	for _, raw := range strings.Fields(text) {
		raw = strings.Trim(raw, wordPunctuation)
		if raw == "" || strings.ContainsAny(raw, identifierCharacters) || isCamelCase(raw) {
			continue
		}
		for _, word := range strings.FieldsFunc(raw, isNotASCIILetter) {
			word = collapseRepeats(strings.ToLower(word))
			stem := Stem(word)
			if len(word) < MinimumLength || stoplist.has(word, stem) {
				continue
			}
			if _, duplicate := seen[stem]; duplicate {
				continue
			}
			seen[stem] = struct{}{}
			terms = append(terms, Term(word))
		}
	}
	return terms
}

// Same reports whether two terms share a stem: one spelled "Cgroups" names
// the same term as one spelled "cgroup".
func Same(left Term, right Term) bool { return stemOf(left) == stemOf(right) }

// stemOf is the stem of a term however it was spelled.
func stemOf(term Term) string {
	return Stem(collapseRepeats(strings.ToLower(strings.TrimSpace(string(term)))))
}

func isNotASCIILetter(character rune) bool {
	return !((character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z'))
}

// isCamelCase reports a lowercase letter directly followed by an uppercase
// one: an identifier such as ClaudeCodeSession, never prose.
func isCamelCase(word string) bool {
	previousLower := false
	for _, character := range word {
		if previousLower && unicode.IsUpper(character) {
			return true
		}
		previousLower = unicode.IsLower(character)
	}
	return false
}

// collapseRepeats shortens any run of one letter longer than repeatLimit to
// one letter; shorter runs stay, so "committee" is untouched.
func collapseRepeats(word string) string {
	var collapsed strings.Builder
	for start := 0; start < len(word); {
		end := start + 1
		for end < len(word) && word[end] == word[start] {
			end++
		}
		if end-start > repeatLimit {
			collapsed.WriteByte(word[start])
		} else {
			collapsed.WriteString(word[start:end])
		}
		start = end
	}
	return collapsed.String()
}

// wordSet is the stoplist: words and their stems.
type wordSet struct {
	words map[string]struct{}
	stems map[string]struct{}
}

func (set wordSet) has(word string, stem string) bool {
	if _, common := set.words[word]; common {
		return true
	}
	_, common := set.stems[stem]
	return common
}

func loadStoplist(text string) wordSet {
	set := wordSet{words: map[string]struct{}{}, stems: map[string]struct{}{}}
	for _, line := range strings.Split(text, "\n") {
		word := strings.TrimSpace(line)
		if word == "" || strings.HasPrefix(word, stoplistComment) {
			continue
		}
		set.words[word] = struct{}{}
		set.stems[Stem(word)] = struct{}{}
	}
	return set
}
