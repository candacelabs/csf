// Package ifacereturn reports every function and method declaration through
// whose results an interface reaches a caller.
//
// # Running it
//
// In this monorepo, through the CLI, which is the only invocation an operator
// here should need:
//
//	candace style ifacereturn
//
// That wraps tools/check-ifacereturn.sh, which knows where this monorepo's
// modules are and sweeps every one of them in the pinned toolchain container.
//
// For consumers of the published candacelabs/csf module, who have no
// private CLI, the portable form is a go run from a module root:
//
//	go run github.com/candacelabs/csf/tools/ifacereturn/cmd/ifacereturn ./...
//
// It is the wide half of a two-lane enforcement of house rule CS-8, "return
// concrete implementations, only accept interfaces". The narrow lane is a
// lexical gate that blocks CI and deliberately reports only a receiverless
// function returning a bare, repository-declared, I-prefixed interface — five
// restrictions that exist so that every finding has a fix. This lane has one
// exemption and no restrictions: it is type-aware
// (go/types answers "is this an interface" exactly, where a lexer can only
// guess), it reads methods as well as functions, and it reports stdlib and
// third-party interfaces the lexer cannot see.
//
// It therefore reports code that has been ruled correct — sealed sum types,
// hook implementations whose signature a func type fixes, pass-throughs of a
// library's own contract, and the method-position returns that survived the
// 2026-09-02 data-shaped re-audit. That is the point rather than a defect:
// operator directive, 2026-09-02, "i want a ci lint check that checks to see
// if there are any interface return types and flags them". Keeping the ruled
// cases visible is what the lane is for, so it flags and never blocks, and a
// ruled case is answered in the rule's own exceptions record rather than
// silenced here. There is no marker comment and no exclusion list.
//
// Since 2026-09-03 it also descends: a result that is a struct, or a pointer,
// slice, array, map or channel of one, is walked field by field, and an
// interface reached that way is reported with the path that reaches it
// (`NewView returns View.Store, which is the interface subject.IStore`).
// Operator ruling of that date: returning a struct that carries interface-typed
// fields is returning those interfaces, and the wrapper is not a boundary. A
// visited set makes a recursive type terminate and maxDepth caps a wide one; a
// func-typed field is deliberately not followed, because a callback a struct
// carries is a contract rather than an implementation handed over.
//
// Direction is a property of the walk rather than a filter on it: only RESULTS
// are ever walked, so the parameter position CS-8 asks an interface to live in
// is unreachable from here by construction.
//
// The one exemption is error. Go's own contract, implemented by everything,
// matched by errors.Is and errors.As, and returned by roughly every function
// in this tree; CS-8 states it as the rule's single structural exemption and
// this lane inherits it verbatim.
//
// Type parameters are not findings either, and that is a correctness fix
// rather than a policy: types.IsInterface reports true for a type parameter,
// because a type parameter's underlying type is its constraint. `func f[S
// any]() S` returns the caller's type, not an interface the callee chose, and
// reporting it would be reporting the constraint syntax.
package ifacereturn

import (
	"go/ast"
	"go/token"
	"go/types"

	"golang.org/x/tools/go/analysis"
)

// Doc is the analyzer's one-line description, and the text `-help` prints.
const Doc = "report every function or method result whose declared type is an interface (house rule CS-8, flagging lane; error is the only exemption)"

// Finding is one result position through which an interface reaches a caller.
type Finding struct {
	// Pos is the offending result type expression, so a report points at the
	// type rather than at the declaration's name.
	Pos token.Pos
	// Declaration names the function or method, receiver included, in the
	// form a reader would grep for: `NewRealClock`, `(RealClock).NewTimer`.
	Declaration string
	// Position is the 1-based index of this result in the result list, and
	// Arity is how many results there are, so a multi-result signature says
	// which one is meant.
	Position int
	Arity    int
	// Interface is the interface's full name, package path included
	// (`github.com/candacelabs/csf/services/warden.IClock`, `io.Reader`,
	// `any`).
	Interface string
	// Path is how the interface is reached from the result, when it is not the
	// result itself: `View.Store`, or `View.Inner.Store` one level deeper. It
	// is empty for a result that IS the interface.
	//
	// Operator ruling, 2026-09-03: returning a struct that carries
	// interface-typed fields is returning those interfaces. A caller that
	// receives the struct receives every interface reachable from it, so the
	// wrapper is not a boundary and this lane says so by naming the way
	// through.
	Path string
}

// Message is the diagnostic text for one finding, and the string the analyzer
// reports. It names the whole signature position because "returns an
// interface" is not actionable without knowing which result, and it names the
// path because "somewhere inside this struct" is not actionable either.
func (finding Finding) Message() string {
	place := ""
	if finding.Arity > 1 {
		place = " (result " + itoa(finding.Position) + " of " + itoa(finding.Arity) + ")"
	}
	if finding.Path == "" {
		return finding.Declaration + " returns the interface " + finding.Interface + place
	}
	return finding.Declaration + " returns " + finding.Path + ", which is the interface " +
		finding.Interface + place
}

// itoa is strconv.Itoa for small non-negative counts, kept local so this
// package's import list stays the analysis framework plus go/*.
func itoa(value int) string {
	if value == 0 {
		return "0"
	}
	digits := make([]byte, 0, 3)
	for value > 0 {
		digits = append([]byte{byte('0' + value%10)}, digits...)
		value /= 10
	}
	return string(digits)
}

// Analyzer is the go/analysis entry point. Run collects the same findings
// Inspect does — there is one walk, so the analyzer, the command and the tests
// cannot drift apart.
var Analyzer = &analysis.Analyzer{
	Name: "ifacereturn",
	Doc:  Doc,
	Run:  run,
}

// run adapts Inspect to the analysis framework.
//
// Its own `any` result is this lane's first finding and is left standing on
// purpose: the signature belongs to analysis.Analyzer.Run, not to this
// function, which is precisely the library-pass-through class CS-8 exempts by
// ruling and this lane reports anyway.
func run(pass *analysis.Pass) (any, error) {
	for _, finding := range InspectIn(pass.Files, pass.TypesInfo, pass.Pkg) {
		pass.Reportf(finding.Pos, "%s", finding.Message())
	}
	return nil, nil
}

// Inspect walks every function and method declaration in files and returns one
// Finding per interface-typed result, in source order.
//
// info must be the type information for those files; a result whose type
// cannot be resolved is skipped rather than guessed at, because a lint that
// invents a finding from an unresolved type is worse than one that misses it.
func Inspect(files []*ast.File, info *types.Info) []Finding {
	return InspectIn(files, info, nil)
}

// InspectIn is Inspect told which package it is analyzing.
//
// The package decides whether an UNEXPORTED field of a returned struct counts
// as handed over: in-package it is reachable, out-of-package it is not. Inspect
// passes nil, which is the conservative reading — only exported fields are
// followed — and the analyzer passes pass.Pkg.
func InspectIn(files []*ast.File, info *types.Info, scope *types.Package) []Finding {
	var findings []Finding
	for _, file := range files {
		for _, declaration := range file.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if !ok || function.Type == nil || function.Type.Results == nil {
				continue
			}
			findings = append(findings, resultFindings(function, info, scope)...)
		}
	}
	return findings
}

// maxDepth bounds the descent through named structs.
//
// It is a cap rather than a limit anybody has reached: the visited set already
// makes the walk terminate on a recursive type, and this is the second guard,
// for the pathological wide-and-deep tree where termination is not the problem
// and the number of paths is. Six levels is well past anything in this
// repository — the deepest real path measured on landing is two — and a
// diagnostic naming a seven-hop path would not be actionable anyway.
const maxDepth = 6

// resultFindings reports every interface one declaration's results carry.
func resultFindings(function *ast.FuncDecl, info *types.Info, scope *types.Package) []Finding {
	results := flattenResults(function.Type.Results)
	name := declarationName(function)
	var findings []Finding
	for index, expression := range results {
		resultType := info.TypeOf(expression)
		if resultType == nil {
			continue
		}
		for _, reached := range reachableInterfaces(resultType, scope) {
			findings = append(findings, Finding{
				Pos:         expression.Pos(),
				Declaration: name,
				Position:    index + 1,
				Arity:       len(results),
				Interface:   reached.name,
				Path:        reached.path,
			})
		}
	}
	return findings
}

// reached is one interface a result hands over, and the way it is reached.
type reached struct {
	name string
	path string // empty when the result IS the interface
}

// reachableInterfaces walks one result type and returns every interface a
// caller receives through it.
//
// It descends through the wrappers that hand their contents over unchanged — a
// pointer, a slice, an array, a map's key and value, a channel — and through
// the FIELDS of a named or anonymous struct, which is the 2026-09-03 amendment.
// A func-typed field is not followed: a callback the struct carries is a
// contract the caller supplies or invokes rather than an implementation handed
// over, and following one would report every options struct in the tree twice.
//
// Direction is a property of the walk rather than a filter on it: this is only
// ever called on RESULTS, so a parameter of interface type — the position CS-8
// asks for — is never reachable from here.
func reachableInterfaces(resultType types.Type, scope *types.Package) []reached {
	var found []reached
	visited := map[*types.Named]bool{}
	var walk func(current types.Type, path string, depth int)
	walk = func(current types.Type, path string, depth int) {
		if current == nil || depth > maxDepth {
			return
		}
		if isReportable(current) {
			found = append(found, reached{name: types.TypeString(current, nil), path: path})
			return
		}
		if named, ok := types.Unalias(current).(*types.Named); ok {
			if visited[named] {
				return // a recursive type, and one visit is the whole of it
			}
			visited[named] = true
			if structure, isStruct := named.Underlying().(*types.Struct); isStruct {
				owner := named.Obj().Pkg()
				// The path so far when there is one, so a nested struct reads
				// `View.Inner.Store` rather than restarting at `Inner.Store`.
				// At the top of a walk there is no path yet and the type's own
				// name is the honest start of one.
				holder := path
				if holder == "" {
					holder = named.Obj().Name()
				}
				walkFields(structure, holder, depth, owner == scope, walk)
			}
			return
		}
		switch underlying := types.Unalias(current).(type) {
		case *types.Struct:
			// An anonymous struct written in the analyzed file: every field of
			// it is as reachable as the result itself.
			walkFields(underlying, path, depth, true, walk)
		case *types.Pointer:
			walk(underlying.Elem(), path, depth)
		case *types.Slice:
			walk(underlying.Elem(), path, depth)
		case *types.Array:
			walk(underlying.Elem(), path, depth)
		case *types.Chan:
			walk(underlying.Elem(), path, depth)
		case *types.Map:
			walk(underlying.Key(), path, depth)
			walk(underlying.Elem(), path, depth)
		}
	}
	walk(resultType, "", 0)
	return found
}

// walkFields descends into one struct's fields, naming the path as it goes.
//
// `inScope` says whether the struct was declared in the package under analysis.
// An unexported field of a struct from ANOTHER package is not reachable by the
// caller that receives it, so it is not something the declaration hands over,
// and following it is how this lane went from 407 findings to 9,459 the first
// time it was run: every protobuf message carries a `state
// protoimpl.MessageState`, every metric bundle an unexported counter, and none
// of it is anything a caller could touch. Exported fields of a foreign struct
// ARE reachable, and are still followed.
func walkFields(
	structure *types.Struct,
	holder string,
	depth int,
	inScope bool,
	walk func(current types.Type, path string, depth int),
) {
	for index := 0; index < structure.NumFields(); index++ {
		field := structure.Field(index)
		if !field.Exported() && !inScope {
			continue
		}
		if _, isFunc := field.Type().Underlying().(*types.Signature); isFunc {
			continue
		}
		walk(field.Type(), holder+"."+field.Name(), depth+1)
	}
}

// flattenResults expands a result list into one type expression per result, so
// that `(a, b Store)` counts as two and the reported positions match what a
// caller destructures.
func flattenResults(results *ast.FieldList) []ast.Expr {
	var expressions []ast.Expr
	for _, field := range results.List {
		repeat := len(field.Names)
		if repeat == 0 {
			repeat = 1
		}
		for index := 0; index < repeat; index++ {
			expressions = append(expressions, field.Type)
		}
	}
	return expressions
}

// isReportable decides whether one resolved result type is a finding: an
// interface that is neither error nor a type parameter.
func isReportable(resultType types.Type) bool {
	if !types.IsInterface(resultType) {
		return false
	}
	if _, ok := types.Unalias(resultType).(*types.TypeParam); ok {
		return false
	}
	return !isError(resultType)
}

// isError reports whether resultType is the predeclared error, identified by
// its object living in no package rather than by its spelling — a local
// `type error interface{...}` is a different type and stays reportable.
func isError(resultType types.Type) bool {
	named, ok := types.Unalias(resultType).(*types.Named)
	if !ok {
		return false
	}
	object := named.Obj()
	return object != nil && object.Pkg() == nil && object.Name() == "error"
}

// declarationName renders a declaration the way a reader greps for it: bare
// for a function, receiver-qualified for a method.
func declarationName(function *ast.FuncDecl) string {
	if function.Recv == nil || len(function.Recv.List) == 0 {
		return function.Name.Name
	}
	return "(" + types.ExprString(function.Recv.List[0].Type) + ")." + function.Name.Name
}
