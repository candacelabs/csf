// Copyright 2026 Candace Labs

// Package deadcode finds the source functions no program in a module can
// reach, and removes the ones that are dead by construction.
//
// Reachability is golang.org/x/tools' deadcode analysis: every main package
// and every test executable is a root, rapid type analysis computes what they
// reach, and a function declared in source but reached by none is dead.
// Functions in generated files and interface marker methods are never
// reported.
//
// Dead is not the same as removable. An exported function of a library
// package is that package's contract; a caller outside the module, or one
// not yet written, may need it, so it is reported and kept. Everything no
// other package could ever call is removable: an unexported function, any
// function of a main package, and any function in a _test.go file.
package deadcode

import (
	"bytes"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"maps"
	"os"
	"reflect"
	"slices"
	"strings"

	"golang.org/x/tools/go/callgraph/rta"
	"golang.org/x/tools/go/packages"
	"golang.org/x/tools/go/ssa"
	"golang.org/x/tools/go/ssa/ssautil"
	"golang.org/x/tools/imports"
)

const (
	mainPackage        = "main"
	testSuffix         = "_test.go"
	externalTestSuffix = "_test"
	initName           = "init"
	mainName           = "main"
)

// ownPackage is this package's import path, read from its own types so a
// move or rename keeps the remover off its own source.
var ownPackage = reflect.TypeFor[Function]().PkgPath()

var (
	// ErrNoMains reports patterns that hold no main package and no test, so
	// nothing is a root and every function would read as dead.
	ErrNoMains = errors.New("deadcode: the patterns hold no main package or test to reach anything from")
	// ErrLoad reports packages that do not load or type-check.
	ErrLoad = errors.New("deadcode: packages do not type-check")
	// ErrNotFound reports a function Remove cannot find at its position.
	ErrNotFound = errors.New("deadcode: no function declaration at the recorded position")
	// ErrProtected reports a removal of this package's own source.
	ErrProtected = errors.New("deadcode: the remover never edits its own package")
)

// Function is one source function nothing reaches.
type Function struct {
	// Position is where its name is declared.
	Position token.Position
	// Name is Receiver.Method or Function.
	Name string
	// Package is its import path.
	Package string
	// Exported reports an exported name on an exported (or no) receiver.
	Exported bool
	// InMain reports a function of a main package.
	InMain bool
	// InTest reports a function declared in a _test.go file.
	InTest bool
	// Satisfies reports a method its type needs to satisfy an interface
	// it is converted to: nothing calls it, yet the build does.
	Satisfies bool
}

// Removable reports a function no other package could ever call, so its
// being unreachable makes it dead for every caller there will ever be, and
// that no interface its type is converted to needs.
func (function Function) Removable() bool {
	return !function.Satisfies && (!function.Exported || function.InMain || function.InTest)
}

// Protected reports a function of this package: the remover never edits its
// own source, whatever the analysis says about it.
func (function Function) Protected() bool {
	return function.Package == ownPackage || function.Package == ownPackage+externalTestSuffix
}

// identity names a function across passes, whose positions shift as
// declarations above it are removed.
func (function Function) identity() string {
	return function.Package + " " + function.Position.Filename + " " + function.Name
}

// String is the function as deadcode prints it, path:line:col: name.
func (function Function) String() string {
	return fmt.Sprintf("%s: %s", function.Position, function.Name)
}

// Find loads patterns, relative to dir, with their tests and returns every
// source function of the module no main package or test reaches, ordered by
// file and line.
func Find(dir string, patterns ...string) ([]Function, error) {
	config := &packages.Config{Dir: dir, Mode: packages.LoadAllSyntax | packages.NeedModule, Tests: true}
	initial, err := packages.Load(config, patterns...)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrLoad, err)
	}
	if count := packages.PrintErrors(initial); count > 0 {
		return nil, fmt.Errorf("%w: %d errors", ErrLoad, count)
	}
	program, built := ssautil.AllPackages(initial, ssa.InstantiateGenerics)
	program.Build()
	mains := ssautil.MainPackages(built)
	if len(mains) == 0 {
		return nil, ErrNoMains
	}
	roots := make([]*ssa.Function, 0, 2*len(mains))
	for _, main := range mains {
		roots = append(roots, main.Func(initName), main.Func(mainName))
	}
	// Pass one reaches from the programs alone. The exported library API it
	// cannot reach is kept, so pass two adds it as roots: what only kept API
	// calls is live, and only what pass two still cannot reach is dead.
	declared := sourceFunctions(program, initial)
	reachable := reachedPositions(program, rta.Analyze(roots, false))
	var unreachedAPI []token.Position
	for position, function := range declared {
		if !reachable[position] && function.kept {
			roots = append(roots, function.value)
			unreachedAPI = append(unreachedAPI, position)
		}
	}
	reachable = reachedPositions(program, rta.Analyze(roots, false))
	for _, position := range unreachedAPI {
		reachable[position] = false
	}
	conforms := conformance(allInterfaces(initial))
	modules := modulePaths(initial)
	found := map[token.Position]Function{}
	packages.Visit(initial, nil, func(loaded *packages.Package) {
		if loaded.Module == nil || !modules[loaded.Module.Path] {
			return
		}
		markers := interfacesOf(loaded.Types)
		for _, file := range loaded.Syntax {
			if ast.IsGenerated(file) {
				continue
			}
			for _, declaration := range file.Decls {
				function, ok := declaration.(*ast.FuncDecl)
				if !ok {
					continue
				}
				object, ok := loaded.TypesInfo.Defs[function.Name].(*types.Func)
				if !ok {
					continue
				}
				position := program.Fset.Position(function.Name.Pos())
				if reachable[position] || isMarker(object, markers) {
					continue
				}
				found[position] = Function{
					Position:  position,
					Name:      declaredName(function),
					Package:   loaded.Types.Path(),
					Exported:  exported(function),
					InMain:    loaded.Name == mainPackage,
					InTest:    strings.HasSuffix(position.Filename, testSuffix),
					Satisfies: conforms(object),
				}
			}
		}
	})
	return slices.SortedFunc(maps.Values(found), func(a Function, b Function) int {
		if a.Position.Filename != b.Position.Filename {
			return strings.Compare(a.Position.Filename, b.Position.Filename)
		}
		return a.Position.Line - b.Position.Line
	}), nil
}

// declaredFunction is a source function's SSA value and whether it is kept
// API: exported, in a library package, outside a test file.
type declaredFunction struct {
	value *ssa.Function
	kept  bool
}

// sourceFunctions are the module's source-declared functions by the position
// of their name.
func sourceFunctions(program *ssa.Program, initial []*packages.Package) map[token.Position]declaredFunction {
	modules := modulePaths(initial)
	declared := map[token.Position]declaredFunction{}
	packages.Visit(initial, nil, func(loaded *packages.Package) {
		if loaded.Module == nil || !modules[loaded.Module.Path] {
			return
		}
		for _, file := range loaded.Syntax {
			for _, declaration := range file.Decls {
				function, ok := declaration.(*ast.FuncDecl)
				if !ok {
					continue
				}
				object, ok := loaded.TypesInfo.Defs[function.Name].(*types.Func)
				if !ok {
					continue
				}
				value := program.FuncValue(object)
				if value == nil {
					continue
				}
				position := program.Fset.Position(function.Name.Pos())
				kept := exported(function) && loaded.Name != mainPackage && !strings.HasSuffix(position.Filename, testSuffix)
				if previous, seen := declared[position]; !seen || (kept && !previous.kept) {
					declared[position] = declaredFunction{value: value, kept: kept}
				}
			}
		}
	})
	return declared
}

// reachedPositions is the declaration position of every reached function.
// A test variant of a package is a distinct SSA package; positions merge
// them, so a function reached in any variant is reached.
func reachedPositions(program *ssa.Program, result *rta.Result) map[token.Position]bool {
	reached := map[token.Position]bool{}
	for function := range result.Reachable {
		if function.Pos().IsValid() {
			reached[program.Fset.Position(function.Pos())] = true
		}
	}
	return reached
}

// modulePaths are the modules of the loaded patterns: functions of
// dependencies are never reported.
func modulePaths(initial []*packages.Package) map[string]bool {
	modules := map[string]bool{}
	for _, loaded := range initial {
		if loaded.Module != nil && loaded.Module.Path != "" {
			modules[loaded.Module.Path] = true
		}
	}
	return modules
}

// allInterfaces are the named interfaces of every loaded package and its
// dependencies.
func allInterfaces(initial []*packages.Package) []*types.Interface {
	var interfaces []*types.Interface
	packages.Visit(initial, nil, func(loaded *packages.Package) {
		if loaded.Types != nil {
			interfaces = append(interfaces, interfacesOf(loaded.Types)...)
		}
	})
	return interfaces
}

// conformance reports, for a method, whether some interface its receiver
// type implements declares the method. A conversion the build needs may have
// no runtime effect at all (var _ I = T{}), so the program's runtime types
// cannot decide it; implementing the interface is the conservative answer.
func conformance(interfaces []*types.Interface) func(function *types.Func) bool {
	return func(function *types.Func) bool {
		receiver := function.Type().(*types.Signature).Recv()
		if receiver == nil {
			return false
		}
		value := receiver.Type()
		if pointer, ok := value.(*types.Pointer); ok {
			value = pointer.Elem()
		}
		pointer := types.NewPointer(value)
		for _, declared := range interfaces {
			if declared.NumMethods() == 0 || !types.Implements(pointer, declared) {
				continue
			}
			for method := range declared.Methods() {
				if method.Name() == function.Name() {
					return true
				}
			}
		}
		return false
	}
}

// interfacesOf are the interfaces a package declares, for marker methods.
func interfacesOf(pkg *types.Package) []*types.Interface {
	var interfaces []*types.Interface
	scope := pkg.Scope()
	for _, name := range scope.Names() {
		if declared, ok := scope.Lookup(name).(*types.TypeName); ok && types.IsInterface(declared.Type()) {
			interfaces = append(interfaces, declared.Type().Underlying().(*types.Interface))
		}
	}
	return interfaces
}

// isMarker reports an unexported, parameterless, resultless method an
// interface of its package declares: it exists to satisfy the interface and
// is never called.
func isMarker(function *types.Func, interfaces []*types.Interface) bool {
	signature := function.Type().(*types.Signature)
	if signature.Recv() == nil || function.Exported() || signature.Params().Len() != 0 || signature.Results().Len() != 0 {
		return false
	}
	for _, declared := range interfaces {
		for method := range declared.Methods() {
			if method.Name() == function.Name() {
				return true
			}
		}
	}
	return false
}

// declaredName is Receiver.Method or Function.
func declaredName(function *ast.FuncDecl) string {
	if receiver := receiverName(function); receiver != "" {
		return receiver + "." + function.Name.Name
	}
	return function.Name.Name
}

// receiverName is the receiver's type name, without pointer or type
// parameters, or empty for a function.
func receiverName(function *ast.FuncDecl) string {
	if function.Recv == nil || len(function.Recv.List) == 0 {
		return ""
	}
	expression := function.Recv.List[0].Type
	for {
		switch typed := expression.(type) {
		case *ast.StarExpr:
			expression = typed.X
		case *ast.IndexExpr:
			expression = typed.X
		case *ast.IndexListExpr:
			expression = typed.X
		case *ast.Ident:
			return typed.Name
		default:
			return ""
		}
	}
}

// exported reports a name another package can call: exported, and on an
// exported receiver type when it is a method.
func exported(function *ast.FuncDecl) bool {
	if !function.Name.IsExported() {
		return false
	}
	receiver := receiverName(function)
	return receiver == "" || ast.IsExported(receiver)
}

// Remove deletes the declarations of functions, with their doc comments, and
// returns each rewritten file's new content keyed by path, formatted and with
// the imports nothing uses any more removed. It writes nothing.
func Remove(functions []Function) (map[string][]byte, error) {
	byFile := map[string][]Function{}
	for _, function := range functions {
		if function.Protected() {
			return nil, fmt.Errorf("%w: %s", ErrProtected, function)
		}
		byFile[function.Position.Filename] = append(byFile[function.Position.Filename], function)
	}
	rewritten := map[string][]byte{}
	for path, inFile := range byFile {
		content, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		updated, err := removeFromFile(path, content, inFile)
		if err != nil {
			return nil, err
		}
		rewritten[path] = updated
	}
	return rewritten, nil
}

// removeFromFile cuts each function's declaration, from its doc comment to
// its closing brace and the line break after it, then formats the file and
// prunes its imports.
func removeFromFile(path string, content []byte, functions []Function) ([]byte, error) {
	files := token.NewFileSet()
	file, err := parser.ParseFile(files, path, content, parser.ParseComments)
	if err != nil {
		return nil, err
	}
	type span struct{ start, end int }
	var spans []span
	for _, function := range functions {
		declaration := declarationAt(files, file, function.Position)
		if declaration == nil {
			return nil, fmt.Errorf("%w: %s", ErrNotFound, function)
		}
		start := declaration.Pos()
		if declaration.Doc != nil {
			start = declaration.Doc.Pos()
		}
		begin, end := files.Position(start).Offset, files.Position(declaration.End()).Offset
		for end < len(content) && (content[end] == '\n' || content[end] == '\r') {
			end++
		}
		spans = append(spans, span{begin, end})
	}
	slices.SortFunc(spans, func(a span, b span) int { return b.start - a.start })
	updated := bytes.Clone(content)
	for _, cut := range spans {
		updated = append(updated[:cut.start], updated[cut.end:]...)
	}
	return imports.Process(path, updated, &imports.Options{Comments: true, TabIndent: true, TabWidth: 8, FormatOnly: false})
}

// declarationAt is the function declaration whose name sits at position.
func declarationAt(files *token.FileSet, file *ast.File, position token.Position) *ast.FuncDecl {
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok {
			continue
		}
		at := files.Position(function.Name.Pos())
		if at.Line == position.Line && at.Column == position.Column {
			return function
		}
	}
	return nil
}
