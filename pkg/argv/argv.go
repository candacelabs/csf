// Copyright 2026 Candace Labs

// Package argv reads another program's argument vector without knowing that
// program's option table: whether a POSIX short flag or a GNU long option is
// present, the way getopt and getopt_long would see it.
//
// It is a pure library. It starts no goroutines and crosses no boundary.
//
// Knowing no option table has one limit, and it is the price of reading any
// program: a value is indistinguishable from a flag. In "-ofile" where -o
// takes a value, the bundle is read as -o -f -i -l -e; in "-p -f" where -p
// takes a value, "-f" is read as a flag. A caller that must be exact for one
// program parses with that program's table instead.
package argv

import (
	"slices"
	"strings"
)

// The spellings getopt_long gives special meaning to.
const (
	// Terminator ends the options: every argument after it is an operand.
	Terminator     = "--"
	shortPrefix    = "-"
	longPrefix     = "--"
	valueSeparator = "="
)

// Flag names one option in its short form, its long form or both.
type Flag struct {
	// Short is the option's letter, as in -f; zero when it has none.
	Short rune
	// Long is the option's name without its dashes, as in full for --full;
	// empty when it has none.
	Long string
}

// HasFlag reports whether arguments carry flag before the terminator: the
// short form alone (-f) or bundled with other short flags (-af, -fl), or the
// long form alone (--full) or with an attached value (--full=yes). A zero
// Flag matches nothing; "-" alone is an operand (standard input), not a flag.
func HasFlag(arguments []string, flag Flag) bool {
	options := arguments
	if end := slices.Index(arguments, Terminator); end >= 0 {
		options = arguments[:end]
	}
	return slices.ContainsFunc(options, flag.matches)
}

// matches reports whether one argument spells the flag.
func (flag Flag) matches(argument string) bool {
	if name, long := strings.CutPrefix(argument, longPrefix); long {
		name, _, _ = strings.Cut(name, valueSeparator)
		return flag.Long != "" && name == flag.Long
	}
	if letters, short := strings.CutPrefix(argument, shortPrefix); short {
		return flag.Short != 0 && strings.ContainsRune(letters, flag.Short)
	}
	return false
}
