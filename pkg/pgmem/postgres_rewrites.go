// Copyright 2026 Candace Labs

package pgmem

import (
	"strings"

	pgquery "github.com/pganalyze/pg_query_go/v6"
)

// PostgreSQL constructs the embedded engine cannot execute as written. Each is
// rewritten on the parsed AST, never on SQL text, into an equivalent the engine
// accepts; COMPATIBILITY.md records the remaining divergences.

// noopStatement stands in for DDL that only names a PostgreSQL catalog object
// the engine does not model: an enum type.
const noopStatement = "SELECT 0 WHERE 0"

const (
	catalogSchemaName = "pg_catalog"
	positionFunction  = "position"
	instrFunction     = "instr"
)

// Engine functions that implement PostgreSQL casts with PostgreSQL's value
// semantics: integer casts round, float casts read Infinity and NaN, boolean
// casts read PostgreSQL's boolean spellings.
const (
	castIntegerFunctionName = "__pgmem_cast_integer"
	castRealFunctionName    = "__pgmem_cast_real"
	castBooleanFunctionName = "__pgmem_cast_boolean"
)

var castFunctions = map[string]string{
	"int2": castIntegerFunctionName, "int4": castIntegerFunctionName, "int8": castIntegerFunctionName,
	"integer": castIntegerFunctionName, "bigint": castIntegerFunctionName, "smallint": castIntegerFunctionName,
	"float4": castRealFunctionName, "float8": castRealFunctionName, "numeric": castRealFunctionName,
	"real": castRealFunctionName, "decimal": castRealFunctionName,
	"bool": castBooleanFunctionName, "boolean": castBooleanFunctionName,
}

// timestampFunctions are PostgreSQL's current-time functions; the engine
// evaluates all of them as CURRENT_TIMESTAMP.
var timestampFunctions = map[string]bool{
	"now": true, "statement_timestamp": true, "transaction_timestamp": true, "clock_timestamp": true,
}

// regexOperators maps PostgreSQL's POSIX match operators to whether the match
// ignores case and whether the operator negates it.
var regexOperators = map[string]struct{ insensitive, negated bool }{
	"~": {false, false}, "~*": {true, false}, "!~": {false, true}, "!~*": {true, true},
}

// rewriteExpression replaces one expression node in place.
func rewriteExpression(node *pgquery.Node) {
	switch {
	case node.GetTypeCast() != nil:
		rewriteCast(node, node.GetTypeCast())
	case node.GetAExpr() != nil:
		rewriteRegexOperator(node, node.GetAExpr())
	case node.GetFuncCall() != nil:
		rewriteFunctionCall(node, node.GetFuncCall())
	}
}

// rewriteCast keeps a parameter bare (sqlc's `$2::text`), routes numeric and
// boolean casts through engine functions with PostgreSQL semantics, and drops
// every other cast: the engine is dynamically typed, so a cast to text, a
// timestamp or an enum type carries nothing it could enforce.
func rewriteCast(node *pgquery.Node, cast *pgquery.TypeCast) {
	if parameter := cast.GetArg().GetParamRef(); parameter != nil {
		node.Node = &pgquery.Node_ParamRef{ParamRef: parameter}
		return
	}
	if function, found := castFunctions[typeName(cast.GetTypeName())]; found && len(cast.GetTypeName().GetArrayBounds()) == 0 {
		node.Node = pgquery.MakeFuncCallNode([]*pgquery.Node{pgquery.MakeStrNode(function)}, []*pgquery.Node{cast.GetArg()}, cast.GetLocation()).Node
		return
	}
	node.Node = cast.GetArg().GetNode()
}

func typeName(name *pgquery.TypeName) string {
	names := name.GetNames()
	if len(names) == 0 {
		return ""
	}
	return strings.ToLower(names[len(names)-1].GetString_().GetSval())
}

func rewriteRegexOperator(node *pgquery.Node, expression *pgquery.A_Expr) {
	if expression.GetKind() != pgquery.A_Expr_Kind_AEXPR_OP || len(expression.GetName()) != 1 {
		return
	}
	operator, found := regexOperators[expression.GetName()[0].GetString_().GetSval()]
	if !found {
		return
	}
	insensitive := int64(0)
	if operator.insensitive {
		insensitive = 1
	}
	match := pgquery.MakeFuncCallNode(
		[]*pgquery.Node{pgquery.MakeStrNode(regexMatchFunctionName)},
		[]*pgquery.Node{expression.GetLexpr(), expression.GetRexpr(), pgquery.MakeAConstIntNode(insensitive, expression.GetLocation())},
		expression.GetLocation())
	if operator.negated {
		match = pgquery.MakeBoolExprNode(pgquery.BoolExprType_NOT_EXPR, []*pgquery.Node{match}, expression.GetLocation())
	}
	node.Node = match.Node
}

func rewriteFunctionCall(node *pgquery.Node, call *pgquery.FuncCall) {
	name, builtin := postgresFunctionName(call)
	if !builtin {
		return
	}
	switch {
	case timestampFunctions[name] && len(call.GetArgs()) == 0:
		node.Node = &pgquery.Node_SqlvalueFunction{SqlvalueFunction: &pgquery.SQLValueFunction{
			Op: pgquery.SQLValueFunctionOp_SVFOP_CURRENT_TIMESTAMP, Typmod: -1, Location: call.GetLocation(),
		}}
	case name == positionFunction && len(call.GetArgs()) == 2:
		// position(needle IN haystack) parses as position(haystack, needle),
		// the engine's instr argument order.
		call.Funcname = []*pgquery.Node{pgquery.MakeStrNode(instrFunction)}
		call.Funcformat = pgquery.CoercionForm_COERCE_EXPLICIT_CALL
	case postgresFunctionRewrites[name] != "":
		call.Funcname = []*pgquery.Node{pgquery.MakeStrNode(postgresFunctionRewrites[name])}
	}
}

// postgresFunctionName returns the lower-cased name of an unqualified or
// pg_catalog-qualified function call.
func postgresFunctionName(call *pgquery.FuncCall) (string, bool) {
	parts := call.GetFuncname()
	switch len(parts) {
	case 1:
		return strings.ToLower(parts[0].GetString_().GetSval()), true
	case 2:
		if strings.EqualFold(parts[0].GetString_().GetSval(), catalogSchemaName) {
			return strings.ToLower(parts[1].GetString_().GetSval()), true
		}
	}
	return "", false
}

// translateStatement handles the statements that need more than an expression
// rewrite: enum type DDL, which names a catalog object the engine does not
// model. Columns of an enum type keep the type's name as their declared type;
// the engine stores the label text. It reports false for every statement it
// leaves to the deparser.
func translateStatement(parsed *pgquery.ParseResult) (string, bool) {
	statement := parsed.GetStmts()[0].GetStmt()
	switch {
	case statement.GetCreateEnumStmt() != nil, statement.GetAlterEnumStmt() != nil:
		return noopStatement, true
	case statement.GetDropStmt() != nil && statement.GetDropStmt().GetRemoveType() == pgquery.ObjectType_OBJECT_TYPE:
		return noopStatement, true
	}
	return "", false
}
