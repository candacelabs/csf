// Copyright 2026 Candace Labs

package scip

import (
	"fmt"
	"strings"
)

// Suffix is the grammar's node-kind marker: the character that ends a symbol
// descriptor and says what the descriptor names.
type Suffix int

// The descriptor suffixes the SCIP symbol grammar defines.
const (
	SuffixUnspecified Suffix = iota
	SuffixNamespace
	SuffixType
	SuffixTerm
	SuffixMethod
	SuffixTypeParameter
	SuffixParameter
	SuffixMeta
	SuffixMacro
)

// String returns the grammar's name for the suffix.
func (s Suffix) String() string {
	switch s {
	case SuffixNamespace:
		return "namespace"
	case SuffixType:
		return "type"
	case SuffixTerm:
		return "term"
	case SuffixMethod:
		return "method"
	case SuffixTypeParameter:
		return "type_parameter"
	case SuffixParameter:
		return "parameter"
	case SuffixMeta:
		return "meta"
	case SuffixMacro:
		return "macro"
	default:
		return "unspecified"
	}
}

// Descriptor is one node in a symbol's fully qualified name. Name is the
// identifier and Suffix is the character that ended it in the grammar.
type Descriptor struct {
	Name          string
	Disambiguator string
	Suffix        Suffix
}

// Symbol is a parsed SCIP symbol string. A local symbol has Local set and only
// LocalID; every other symbol has a scheme, a package, and one or more
// descriptors forming its fully qualified name.
type Symbol struct {
	Local   bool
	LocalID string

	Scheme      string
	Manager     string
	Package     string
	Version     string
	Descriptors []Descriptor
}

// ParseSymbol parses a SCIP symbol string per the grammar scip.proto documents:
//
//	<symbol>   ::= <scheme> ' ' <package> ' ' (<descriptor>)+ | 'local ' <local-id>
//	<package>  ::= <manager> ' ' <package-name> ' ' <version>
//
// A space inside a name is escaped as two spaces and a backtick as two
// backticks; a name containing a suffix character is wrapped in backticks.
func ParseSymbol(text string) (Symbol, error) {
	if local, ok := strings.CutPrefix(text, "local "); ok {
		if local == "" {
			return Symbol{}, fmt.Errorf("scip: local symbol has no id: %q", text)
		}
		return Symbol{Local: true, LocalID: local}, nil
	}
	scheme, rest := takeEscapedField(text)
	if scheme == "" {
		return Symbol{}, fmt.Errorf("scip: symbol has no scheme: %q", text)
	}
	manager, rest := takeEscapedField(rest)
	packageName, rest := takeEscapedField(rest)
	version, rest := takeEscapedField(rest)
	if manager == "" || packageName == "" || version == "" || rest == "" {
		return Symbol{}, fmt.Errorf("scip: symbol is missing a package or descriptor: %q", text)
	}
	descriptors, err := parseDescriptors(rest)
	if err != nil {
		return Symbol{}, fmt.Errorf("scip: %w in %q", err, text)
	}
	return Symbol{
		Scheme:      scheme,
		Manager:     manager,
		Package:     packageName,
		Version:     version,
		Descriptors: descriptors,
	}, nil
}

// PackagePath is the package part of the symbol, "manager/name@version".
func (s Symbol) PackagePath() string {
	return fmt.Sprintf("%s/%s@%s", s.Manager, s.Package, s.Version)
}

// takeEscapedField reads the next space-separated field, where two spaces stand
// for one. It returns the field and the rest of the string after the separator.
func takeEscapedField(s string) (string, string) {
	var field strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] != ' ' {
			field.WriteByte(s[i])
			continue
		}
		if i+1 < len(s) && s[i+1] == ' ' {
			field.WriteByte(' ')
			i++
			continue
		}
		return field.String(), s[i+1:]
	}
	return field.String(), ""
}

// parseDescriptors reads the descriptor run at the end of a symbol, one
// descriptor per node, until the string is empty.
func parseDescriptors(s string) ([]Descriptor, error) {
	var descriptors []Descriptor
	for len(s) > 0 {
		switch s[0] {
		case '[':
			end := strings.IndexByte(s, ']')
			if end < 0 {
				return nil, fmt.Errorf("unterminated type parameter")
			}
			descriptors = append(descriptors, Descriptor{Name: s[1:end], Suffix: SuffixTypeParameter})
			s = s[end+1:]
		case '(':
			end := strings.IndexByte(s, ')')
			if end < 0 {
				return nil, fmt.Errorf("unterminated parameter")
			}
			descriptors = append(descriptors, Descriptor{Name: s[1:end], Suffix: SuffixParameter})
			s = s[end+1:]
		default:
			name, rest := takeName(s)
			if len(rest) == 0 {
				return nil, fmt.Errorf("descriptor %q has no suffix", name)
			}
			descriptor, remaining, err := descriptorWithSuffix(name, rest)
			if err != nil {
				return nil, err
			}
			descriptors = append(descriptors, descriptor)
			s = remaining
		}
	}
	return descriptors, nil
}

// descriptorWithSuffix reads the suffix that follows a name. A method's suffix
// is "(" disambiguator? ")" ".", so it consumes the closing parenthesis and the
// trailing dot for the method.
func descriptorWithSuffix(name, rest string) (Descriptor, string, error) {
	if rest[0] == '(' {
		end := strings.IndexByte(rest, ')')
		if end < 0 {
			return Descriptor{}, "", fmt.Errorf("unterminated method %q", name)
		}
		if end+1 >= len(rest) || rest[end+1] != '.' {
			return Descriptor{}, "", fmt.Errorf("method %q is missing its trailing dot", name)
		}
		return Descriptor{Name: name, Disambiguator: rest[1:end], Suffix: SuffixMethod}, rest[end+2:], nil
	}
	var suffix Suffix
	switch rest[0] {
	case '/':
		suffix = SuffixNamespace
	case '#':
		suffix = SuffixType
	case '.':
		suffix = SuffixTerm
	case ':':
		suffix = SuffixMeta
	case '!':
		suffix = SuffixMacro
	default:
		return Descriptor{}, "", fmt.Errorf("unknown suffix %q after %q", rest[0], name)
	}
	return Descriptor{Name: name, Suffix: suffix}, rest[1:], nil
}

// takeName reads a descriptor name, either backtick-wrapped or a run of
// characters up to the next suffix character.
func takeName(s string) (string, string) {
	if s[0] == '`' {
		var name strings.Builder
		for i := 1; i < len(s); i++ {
			if s[i] != '`' {
				name.WriteByte(s[i])
				continue
			}
			if i+1 < len(s) && s[i+1] == '`' {
				name.WriteByte('`')
				i++
				continue
			}
			return name.String(), s[i+1:]
		}
		return name.String(), ""
	}
	for i := 0; i < len(s); i++ {
		if isSuffixCharacter(s[i]) {
			return s[:i], s[i:]
		}
	}
	return s, ""
}

func isSuffixCharacter(c byte) bool {
	switch c {
	case '/', '#', '.', ':', '!', '(', '[':
		return true
	default:
		return false
	}
}
