// Copyright 2026 Candace Labs

// Package textbound cuts text to a byte bound without splitting a character.
// A cut at a raw byte offset can land inside a multi-byte UTF-8 sequence, and
// the half character it leaves is invalid UTF-8: a protobuf string field then
// refuses the whole message it sits in. On 2026-10-05 that made the harness
// chat page unable to connect for any session whose transcript held such a
// cut.
package textbound

import (
	"strings"
	"unicode/utf8"
)

// Ellipsis marks text that was cut.
const Ellipsis = "…"

// Prefix is text cut to at most limit bytes on a character boundary, with
// Ellipsis appended when anything was cut. Invalid UTF-8 already in text is
// replaced, so the result is always valid. A limit below one cuts everything.
func Prefix(text string, limit int) string {
	text = strings.ToValidUTF8(text, string(utf8.RuneError))
	if len(text) <= limit {
		return text
	}
	cut := max(limit, 0)
	for cut > 0 && !utf8.RuneStart(text[cut]) {
		cut--
	}
	return text[:cut] + Ellipsis
}
