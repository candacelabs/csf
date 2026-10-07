// Copyright 2026 Candace Labs

package terms

import "strings"

// Stem is the Porter stem of word: M. F. Porter, "An algorithm for suffix
// stripping", Program 14(3):130-137, 1980, implemented step for step as the
// paper states it. word is lowercase ASCII letters; anything else is returned
// unchanged. A stem is not a word ("analogies" and "analogy" both stem to
// "analogi"); it is the key two inflections of one word share.
func Stem(word string) string {
	if len(word) <= 2 || !isASCIILetters(word) {
		return word
	}
	word = stepOneA(word)
	word = stepOneB(word)
	word = stepOneC(word)
	word = replaceSuffix(word, stepTwo, 0)
	word = replaceSuffix(word, stepThree, 0)
	word = stepFour(word)
	word = stepFiveA(word)
	return stepFiveB(word)
}

func isASCIILetters(word string) bool {
	for index := range len(word) {
		if word[index] < 'a' || word[index] > 'z' {
			return false
		}
	}
	return true
}

// isConsonant is Porter's consonant: not a vowel, and y only after a vowel.
func isConsonant(word string, index int) bool {
	switch word[index] {
	case 'a', 'e', 'i', 'o', 'u':
		return false
	case 'y':
		return index == 0 || !isConsonant(word, index-1)
	}
	return true
}

// measure is Porter's m: the number of vowel-consonant sequences in word.
func measure(word string) int {
	count, index := 0, 0
	for index < len(word) && isConsonant(word, index) {
		index++
	}
	for index < len(word) {
		for index < len(word) && !isConsonant(word, index) {
			index++
		}
		if index >= len(word) {
			break
		}
		count++
		for index < len(word) && isConsonant(word, index) {
			index++
		}
	}
	return count
}

func hasVowel(word string) bool {
	for index := range len(word) {
		if !isConsonant(word, index) {
			return true
		}
	}
	return false
}

// endsDoubleConsonant is Porter's *d.
func endsDoubleConsonant(word string) bool {
	last := len(word) - 1
	return last >= 1 && word[last] == word[last-1] && isConsonant(word, last)
}

// endsCVC is Porter's *o: consonant, vowel, consonant, the last not w, x or y.
func endsCVC(word string) bool {
	last := len(word) - 1
	return last >= 2 && isConsonant(word, last) && !isConsonant(word, last-1) && isConsonant(word, last-2) &&
		!strings.ContainsRune("wxy", rune(word[last]))
}

// suffixRule replaces a suffix when the stem before it has a measure above
// the rule's minimum.
type suffixRule struct {
	suffix      string
	replacement string
}

// The suffix tables of steps 2 and 3, in the paper's order; the first suffix
// that matches is the only one tried.
var (
	stepTwo = []suffixRule{
		{"ational", "ate"}, {"tional", "tion"}, {"enci", "ence"}, {"anci", "ance"}, {"izer", "ize"},
		{"abli", "able"}, {"alli", "al"}, {"entli", "ent"}, {"eli", "e"}, {"ousli", "ous"},
		{"ization", "ize"}, {"ation", "ate"}, {"ator", "ate"}, {"alism", "al"}, {"iveness", "ive"},
		{"fulness", "ful"}, {"ousness", "ous"}, {"aliti", "al"}, {"iviti", "ive"}, {"biliti", "ble"},
	}
	stepThree = []suffixRule{
		{"icate", "ic"}, {"ative", ""}, {"alize", "al"}, {"iciti", "ic"}, {"ical", "ic"}, {"ful", ""}, {"ness", ""},
	}
	stepFourSuffixes = []string{
		"al", "ance", "ence", "er", "ic", "able", "ible", "ant", "ement", "ment", "ent", "ion", "ou", "ism", "ate", "iti", "ous", "ive", "ize",
	}
)

func stepOneA(word string) string {
	switch {
	case strings.HasSuffix(word, "sses"):
		return strings.TrimSuffix(word, "es")
	case strings.HasSuffix(word, "ies"):
		return strings.TrimSuffix(word, "es")
	case strings.HasSuffix(word, "ss"):
		return word
	case strings.HasSuffix(word, "s"):
		return strings.TrimSuffix(word, "s")
	}
	return word
}

func stepOneB(word string) string {
	if strings.HasSuffix(word, "eed") {
		if measure(strings.TrimSuffix(word, "eed")) > 0 {
			return strings.TrimSuffix(word, "d")
		}
		return word
	}
	stripped := word
	switch {
	case strings.HasSuffix(word, "ed") && hasVowel(strings.TrimSuffix(word, "ed")):
		stripped = strings.TrimSuffix(word, "ed")
	case strings.HasSuffix(word, "ing") && hasVowel(strings.TrimSuffix(word, "ing")):
		stripped = strings.TrimSuffix(word, "ing")
	default:
		return word
	}
	switch {
	case strings.HasSuffix(stripped, "at"), strings.HasSuffix(stripped, "bl"), strings.HasSuffix(stripped, "iz"):
		return stripped + "e"
	case endsDoubleConsonant(stripped) && !strings.ContainsRune("lsz", rune(stripped[len(stripped)-1])):
		return stripped[:len(stripped)-1]
	case measure(stripped) == 1 && endsCVC(stripped):
		return stripped + "e"
	}
	return stripped
}

func stepOneC(word string) string {
	if strings.HasSuffix(word, "y") && hasVowel(strings.TrimSuffix(word, "y")) {
		return strings.TrimSuffix(word, "y") + "i"
	}
	return word
}

// replaceSuffix applies the first matching rule of table when the stem's
// measure exceeds minimum.
func replaceSuffix(word string, table []suffixRule, minimum int) string {
	for _, rule := range table {
		if !strings.HasSuffix(word, rule.suffix) {
			continue
		}
		stem := strings.TrimSuffix(word, rule.suffix)
		if measure(stem) > minimum {
			return stem + rule.replacement
		}
		return word
	}
	return word
}

func stepFour(word string) string {
	for _, suffix := range stepFourSuffixes {
		if !strings.HasSuffix(word, suffix) {
			continue
		}
		stem := strings.TrimSuffix(word, suffix)
		if measure(stem) > 1 && (suffix != "ion" || strings.HasSuffix(stem, "s") || strings.HasSuffix(stem, "t")) {
			return stem
		}
		return word
	}
	return word
}

func stepFiveA(word string) string {
	if !strings.HasSuffix(word, "e") {
		return word
	}
	stem := strings.TrimSuffix(word, "e")
	if measure(stem) > 1 || (measure(stem) == 1 && !endsCVC(stem)) {
		return stem
	}
	return word
}

func stepFiveB(word string) string {
	if measure(word) > 1 && endsDoubleConsonant(word) && strings.HasSuffix(word, "l") {
		return word[:len(word)-1]
	}
	return word
}
