// Copyright 2026 Candace Labs

package pgmem

import (
	"database/sql/driver"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"modernc.org/sqlite"
)

// Built-in PostgreSQL functions and operators that the embedded engine lacks.
// The translator rewrites the PostgreSQL spelling into a call to one of these
// engine functions; their names are reserved so no user SQL calls them by
// accident. Each is deterministic, which the engine requires before a
// function may appear in a CHECK constraint or a partial index predicate.
const (
	regexMatchFunctionName = "__pgmem_regex_match"
	rightFunctionName      = "__pgmem_right"
	repeatFunctionName     = "__pgmem_repeat"
)

// postgresFunctionRewrites maps a PostgreSQL scalar function to the engine
// function that implements it.
var postgresFunctionRewrites = map[string]string{
	"right":  rightFunctionName,
	"repeat": repeatFunctionName,
}

// compiledPatterns caches regular expressions by their case-folded form; a
// CHECK constraint evaluates the same pattern once per written row.
var compiledPatterns sync.Map

func init() {
	sqlite.MustRegisterFunction(regexMatchFunctionName, &sqlite.FunctionImpl{
		NArgs: 3, Deterministic: true, Scalar: regexMatch,
	})
	sqlite.MustRegisterFunction(rightFunctionName, &sqlite.FunctionImpl{
		NArgs: 2, Deterministic: true, Scalar: rightCharacters,
	})
	sqlite.MustRegisterFunction(repeatFunctionName, &sqlite.FunctionImpl{
		NArgs: 2, Deterministic: true, Scalar: repeatText,
	})
	sqlite.MustRegisterFunction(castIntegerFunctionName, &sqlite.FunctionImpl{
		NArgs: 1, Deterministic: true, Scalar: castInteger,
	})
	sqlite.MustRegisterFunction(castRealFunctionName, &sqlite.FunctionImpl{
		NArgs: 1, Deterministic: true, Scalar: castReal,
	})
	sqlite.MustRegisterFunction(castBooleanFunctionName, &sqlite.FunctionImpl{
		NArgs: 1, Deterministic: true, Scalar: castBoolean,
	})
}

// castReal implements a cast to a PostgreSQL floating-point or numeric type,
// including the 'Infinity', '-Infinity' and 'NaN' spellings.
func castReal(call *sqlite.FunctionContext, arguments []driver.Value) (driver.Value, error) {
	switch value := arguments[0].(type) {
	case nil:
		return nil, nil
	case int64:
		return float64(value), nil
	case float64:
		return value, nil
	default:
		text := strings.TrimSpace(textValue(value))
		parsed, err := strconv.ParseFloat(text, 64)
		if err != nil {
			return nil, &Error{Code: invalidTextRepresentation, Message: fmt.Sprintf("invalid input syntax for type double precision: %q", text), Cause: err}
		}
		return parsed, nil
	}
}

// castInteger implements a cast to a PostgreSQL integer type: fractions round
// half away from zero, as PostgreSQL rounds, rather than truncating.
func castInteger(call *sqlite.FunctionContext, arguments []driver.Value) (driver.Value, error) {
	switch value := arguments[0].(type) {
	case nil:
		return nil, nil
	case int64:
		return value, nil
	case float64:
		return int64(math.Round(value)), nil
	default:
		text := strings.TrimSpace(textValue(value))
		parsed, err := strconv.ParseInt(text, 10, 64)
		if err != nil {
			return nil, &Error{Code: invalidTextRepresentation, Message: fmt.Sprintf("invalid input syntax for type integer: %q", text), Cause: err}
		}
		return parsed, nil
	}
}

// castBoolean implements a cast to boolean with PostgreSQL's spellings.
func castBoolean(call *sqlite.FunctionContext, arguments []driver.Value) (driver.Value, error) {
	switch value := arguments[0].(type) {
	case nil:
		return nil, nil
	case int64:
		return boolValue(value != 0), nil
	default:
		text := strings.ToLower(strings.TrimSpace(textValue(value)))
		if truth, found := booleanSpellings[text]; found {
			return boolValue(truth), nil
		}
		return nil, &Error{Code: invalidTextRepresentation, Message: fmt.Sprintf("invalid input syntax for type boolean: %q", text)}
	}
}

// invalidTextRepresentation is PostgreSQL's SQLSTATE for a malformed literal.
const invalidTextRepresentation = "22P02"

var booleanSpellings = map[string]bool{
	"t": true, "true": true, "y": true, "yes": true, "on": true, "1": true,
	"f": false, "false": false, "n": false, "no": false, "off": false, "0": false,
}

func boolValue(truth bool) int64 {
	if truth {
		return 1
	}
	return 0
}

// regexMatch implements PostgreSQL's ~ (and ~* when insensitive is 1). The
// patterns are evaluated with Go's RE2 syntax, which covers the POSIX
// bracket expressions, anchors, alternation and bounded repetition that schema
// CHECK constraints use; back-references and lookaround are not supported.
func regexMatch(call *sqlite.FunctionContext, arguments []driver.Value) (driver.Value, error) {
	if arguments[0] == nil || arguments[1] == nil {
		return nil, nil
	}
	subject, pattern := textValue(arguments[0]), textValue(arguments[1])
	if insensitive, _ := arguments[2].(int64); insensitive != 0 {
		pattern = "(?i)" + pattern
	}
	compiled, found := compiledPatterns.Load(pattern)
	if !found {
		parsed, err := regexp.Compile(pattern)
		if err != nil {
			return nil, fmt.Errorf("invalid regular expression %q: %w", pattern, err)
		}
		compiled, _ = compiledPatterns.LoadOrStore(pattern, parsed)
	}
	if compiled.(*regexp.Regexp).MatchString(subject) {
		return int64(1), nil
	}
	return int64(0), nil
}

// rightCharacters implements right(text, n): the last n characters, or all but
// the first -n characters when n is negative.
func rightCharacters(call *sqlite.FunctionContext, arguments []driver.Value) (driver.Value, error) {
	if arguments[0] == nil || arguments[1] == nil {
		return nil, nil
	}
	characters := []rune(textValue(arguments[0]))
	count, ok := arguments[1].(int64)
	if !ok {
		return nil, fmt.Errorf("right: length must be an integer")
	}
	if count < 0 {
		count = max(int64(len(characters))+count, 0)
	}
	count = min(count, int64(len(characters)))
	return string(characters[int64(len(characters))-count:]), nil
}

// repeatText implements repeat(text, n).
func repeatText(call *sqlite.FunctionContext, arguments []driver.Value) (driver.Value, error) {
	if arguments[0] == nil || arguments[1] == nil {
		return nil, nil
	}
	count, ok := arguments[1].(int64)
	if !ok {
		return nil, fmt.Errorf("repeat: count must be an integer")
	}
	return strings.Repeat(textValue(arguments[0]), int(max(count, 0))), nil
}

// textValue reads an engine value as PostgreSQL text.
func textValue(value driver.Value) string {
	if raw, ok := value.([]byte); ok {
		return string(raw)
	}
	return fmt.Sprint(value)
}
