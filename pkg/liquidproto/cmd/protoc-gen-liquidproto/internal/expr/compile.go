// Package expr compiles the small Liquid Proto predicate grammar to Go.
package expr

import (
	"errors"
	"fmt"
	"go/ast"
	"go/constant"
	"go/parser"
	"go/scanner"
	"go/token"
	"regexp"
	"strconv"
	"strings"
)

// Regexp is one validated package-level compiled regexp declaration.
type Regexp struct {
	VarName string
	Pattern string
}

// Program is a compiled predicate.
type Program struct {
	Source  string
	Expr    string
	Regexps []Regexp
}

// Error is a predicate compilation failure located within the source.
type Error struct {
	Source string
	Col    int
	Msg    string
}

func (e *Error) Error() string {
	if e.Col > 0 {
		return fmt.Sprintf("predicate %q: at column %d: %s", e.Source, e.Col, e.Msg)
	}
	return fmt.Sprintf("predicate %q: %s", e.Source, e.Msg)
}

// Compile accepts comparisons, signed integer literals, &&, ||, !, len(), and matches().
func Compile(src string, fieldType Type, receiver, regexpPrefix string) (*Program, error) {
	if strings.TrimSpace(src) == "" {
		return nil, &Error{Source: src, Msg: "predicate is empty"}
	}
	files := token.NewFileSet()
	node, err := parser.ParseExprFrom(files, "predicate", src, 0)
	if err != nil {
		return nil, parseError(src, err)
	}
	compiler := checker{files: files, src: src, field: fieldType, receiver: receiver, regexpPrefix: regexpPrefix}
	result, err := compiler.check(node)
	if err != nil {
		return nil, err
	}
	if result.typ.Kind != KindBool {
		return nil, compiler.errf(node.Pos(), "predicate must evaluate to bool, got %s", result.typ)
	}
	return &Program{Source: src, Expr: result.code, Regexps: compiler.regexps}, nil
}

func parseError(src string, err error) error {
	var list scanner.ErrorList
	if errors.As(err, &list) && len(list) > 0 {
		return &Error{Source: src, Col: list[0].Pos.Column, Msg: "syntax error: " + list[0].Msg}
	}
	return &Error{Source: src, Msg: "syntax error: " + err.Error()}
}

const (
	precUnary = 6
	precAtom  = 8
)

type checker struct {
	files        *token.FileSet
	src          string
	field        Type
	receiver     string
	regexpPrefix string
	regexps      []Regexp
}

type value struct {
	typ     Type
	code    string
	prec    int
	integer constant.Value
}

func (v value) wrap(precedence int) string {
	if v.prec < precedence {
		return "(" + v.code + ")"
	}
	return v.code
}

func (c *checker) errf(pos token.Pos, format string, args ...any) error {
	return &Error{
		Source: c.src,
		Col:    c.files.Position(pos).Column,
		Msg:    fmt.Sprintf(format, args...),
	}
}

func (c *checker) check(expression ast.Expr) (value, error) {
	switch expression := expression.(type) {
	case *ast.ParenExpr:
		inner, err := c.check(expression.X)
		if err != nil {
			return value{}, err
		}
		inner.code = "(" + inner.code + ")"
		inner.prec = precAtom
		return inner, nil
	case *ast.Ident:
		return c.checkIdent(expression)
	case *ast.BasicLit:
		return c.checkLiteral(expression)
	case *ast.UnaryExpr:
		return c.checkUnary(expression)
	case *ast.BinaryExpr:
		return c.checkBinary(expression)
	case *ast.CallExpr:
		return c.checkCall(expression)
	default:
		return value{}, c.errf(expression.Pos(), "%T is not allowed in a refinement predicate", expression)
	}
}

func (c *checker) checkIdent(identifier *ast.Ident) (value, error) {
	switch identifier.Name {
	case "this":
		return value{typ: c.field, code: c.receiver, prec: precAtom}, nil
	case "len", "matches":
		return value{}, c.errf(identifier.Pos(), "%s is a builtin and must be called", identifier.Name)
	default:
		return value{}, c.errf(identifier.Pos(), "unknown identifier %q; the only value in scope is `this`", identifier.Name)
	}
}

func (c *checker) checkLiteral(literal *ast.BasicLit) (value, error) {
	switch literal.Kind {
	case token.INT:
		integer := constant.MakeFromLiteral(literal.Value, token.INT, 0)
		if integer.Kind() == constant.Unknown {
			return value{}, c.errf(literal.Pos(), "malformed integer literal %s", literal.Value)
		}
		return value{typ: typeUntypedInt, code: literal.Value, prec: precAtom, integer: integer}, nil
	case token.STRING:
		if _, err := strconv.Unquote(literal.Value); err != nil {
			return value{}, c.errf(literal.Pos(), "malformed string literal %s", literal.Value)
		}
		return value{typ: typeString, code: literal.Value, prec: precAtom}, nil
	default:
		return value{}, c.errf(literal.Pos(), "%s literals are not part of the predicate grammar", strings.ToLower(literal.Kind.String()))
	}
}

func (c *checker) checkUnary(expression *ast.UnaryExpr) (value, error) {
	if expression.Op == token.SUB {
		operand, err := c.check(expression.X)
		if err != nil {
			return value{}, err
		}
		if operand.typ.Kind != KindUntypedInt || operand.integer == nil {
			return value{}, c.errf(expression.Pos(), "unary - requires an integer literal")
		}
		integer := constant.UnaryOp(token.SUB, operand.integer, 0)
		return value{typ: typeUntypedInt, code: integer.ExactString(), prec: precUnary, integer: integer}, nil
	}
	if expression.Op != token.NOT {
		return value{}, c.errf(expression.Pos(), "unary operator %s is not part of the predicate grammar", expression.Op)
	}
	operand, err := c.check(expression.X)
	if err != nil {
		return value{}, err
	}
	if operand.typ.Kind != KindBool {
		return value{}, c.errf(expression.Pos(), "operator ! requires a bool operand, got %s", operand.typ)
	}
	return value{typ: typeBool, code: "!" + operand.wrap(precUnary), prec: precUnary}, nil
}

func (c *checker) checkBinary(expression *ast.BinaryExpr) (value, error) {
	left, err := c.check(expression.X)
	if err != nil {
		return value{}, err
	}
	right, err := c.check(expression.Y)
	if err != nil {
		return value{}, err
	}
	operator := expression.Op
	precedence := operator.Precedence()
	emit := func(result Type) value {
		return value{
			typ:  result,
			code: left.wrap(precedence) + " " + operator.String() + " " + right.wrap(precedence+1),
			prec: precedence,
		}
	}
	switch operator {
	case token.LAND, token.LOR:
		if left.typ.Kind != KindBool || right.typ.Kind != KindBool {
			return value{}, c.errf(expression.OpPos, "operator %s requires bool operands, got %s and %s", operator, left.typ, right.typ)
		}
		return emit(typeBool), nil
	case token.EQL, token.NEQ, token.LSS, token.LEQ, token.GTR, token.GEQ:
		unified, err := c.unify(expression.OpPos, operator, left, right)
		if err != nil {
			return value{}, err
		}
		ordered := operator != token.EQL && operator != token.NEQ
		if ordered && !unified.Kind.ordered() {
			return value{}, c.errf(expression.OpPos, "operator %s is not defined on %s", operator, unified)
		}
		if !ordered && !unified.Kind.comparable() {
			return value{}, c.errf(expression.OpPos, "operator %s is not defined on %s; compare len(this) instead", operator, unified)
		}
		return emit(typeBool), nil
	default:
		return value{}, c.errf(expression.OpPos, "operator %s is not part of the predicate grammar", operator)
	}
}

func (c *checker) unify(pos token.Pos, operator token.Token, left, right value) (Type, error) {
	switch {
	case left.typ.Kind.untyped() && right.typ.Kind.untyped():
		return typeUntypedInt, nil
	case left.typ.Kind.untyped():
		if err := c.representable(pos, left, right.typ); err != nil {
			return Type{}, err
		}
		return right.typ, nil
	case right.typ.Kind.untyped():
		if err := c.representable(pos, right, left.typ); err != nil {
			return Type{}, err
		}
		return left.typ, nil
	case left.typ.Go != right.typ.Go:
		return Type{}, c.errf(pos, "mismatched types %s and %s for operator %s", left.typ, right.typ, operator)
	default:
		return left.typ, nil
	}
}

func (c *checker) representable(pos token.Pos, literal value, target Type) error {
	if literal.integer == nil {
		return c.errf(pos, "internal error: untyped value without an integer literal")
	}
	integer := constant.ToInt(literal.integer)
	switch target.Kind {
	case KindUint:
		unsigned, exact := constant.Uint64Val(integer)
		if !exact || constant.Sign(integer) < 0 || (target.Bits < 64 && unsigned > (uint64(1)<<target.Bits)-1) {
			return c.errf(pos, "constant %s overflows %s", literal.code, target)
		}
	case KindInt:
		signed, exact := constant.Int64Val(integer)
		if !exact {
			return c.errf(pos, "constant %s overflows %s", literal.code, target)
		}
		if target.Bits < 64 {
			minimum := -(int64(1) << (target.Bits - 1))
			maximum := (int64(1) << (target.Bits - 1)) - 1
			if signed < minimum || signed > maximum {
				return c.errf(pos, "constant %s overflows %s", literal.code, target)
			}
		}
	default:
		return c.errf(pos, "cannot compare integer constant %s with %s", literal.code, target)
	}
	return nil
}

func (c *checker) checkCall(call *ast.CallExpr) (value, error) {
	function, ok := call.Fun.(*ast.Ident)
	if !ok {
		return value{}, c.errf(call.Pos(), "only the builtins len() and matches() may be called")
	}
	switch function.Name {
	case "len":
		return c.checkLen(call)
	case "matches":
		return c.checkMatches(call)
	default:
		return value{}, c.errf(function.Pos(), "unknown builtin %q; only len() and matches() are available", function.Name)
	}
}

func (c *checker) checkLen(call *ast.CallExpr) (value, error) {
	if len(call.Args) != 1 {
		return value{}, c.errf(call.Pos(), "len() takes exactly 1 argument, got %d", len(call.Args))
	}
	argument, err := c.check(call.Args[0])
	if err != nil {
		return value{}, err
	}
	if argument.typ.Kind != KindString && argument.typ.Kind != KindBytes {
		return value{}, c.errf(call.Args[0].Pos(), "len() requires a string or bytes value, got %s", argument.typ)
	}
	return value{typ: typeInt, code: "len(" + argument.code + ")", prec: precAtom}, nil
}

func (c *checker) checkMatches(call *ast.CallExpr) (value, error) {
	if len(call.Args) != 2 {
		return value{}, c.errf(call.Pos(), "matches() takes exactly 2 arguments, got %d", len(call.Args))
	}
	argument, err := c.check(call.Args[0])
	if err != nil {
		return value{}, err
	}
	if argument.typ.Kind != KindString {
		return value{}, c.errf(call.Args[0].Pos(), "matches() requires a string value, got %s", argument.typ)
	}
	literal, ok := call.Args[1].(*ast.BasicLit)
	if !ok || literal.Kind != token.STRING {
		return value{}, c.errf(call.Args[1].Pos(), "the second argument to matches() must be a string literal holding an RE2 pattern")
	}
	pattern, err := strconv.Unquote(literal.Value)
	if err != nil {
		return value{}, c.errf(literal.Pos(), "malformed string literal %s", literal.Value)
	}
	if _, err := regexp.Compile(pattern); err != nil {
		return value{}, c.errf(literal.Pos(), "invalid RE2 pattern %s: %v", literal.Value, err)
	}
	name := fmt.Sprintf("%s%d", c.regexpPrefix, len(c.regexps))
	c.regexps = append(c.regexps, Regexp{VarName: name, Pattern: pattern})
	return value{typ: typeBool, code: name + ".MatchString(" + argument.code + ")", prec: precAtom}, nil
}
