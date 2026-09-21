package csf

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"time"
	"unicode"

	pb "github.com/candacelabs/csf/proto/candace/brainspine/v1"
	"github.com/getkin/kin-openapi/openapi3"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	WatchRegistryID = "brainspine.source-checks.v1"
	watchScope      = "In-process snapshot checks: required files, Go syntax and named interface/parameter declarations, OpenAPI validation with external references disabled. No compilation, regeneration, proof or deployed-behavior claim."
	goExtension     = ".go"
	yamlExtension   = ".yaml"
)

// ImportantSourcePaths is a fresh explicit allowlist, never a recursive scan.
func ImportantSourcePaths() []string {
	return []string{compilerWatchPath, runtimeWatchPath, contractWatchPath, openAPIWatchPath, generatorWatchPath}
}

const (
	compilerWatchPath  = "csf/compiler.go"
	runtimeWatchPath   = "csf/runtime.go"
	contractWatchPath  = "proto/candace/brainspine/v1/brainspine.proto"
	openAPIWatchPath   = "csf/tools/codegen/generated/brainspine.openapi.yaml"
	generatorWatchPath = "pkg/liquidproto/cmd/protoc-gen-liquidproto/internal/gen/gen.go"
)

type sourceRule struct {
	name  string
	check func(ctx context.Context, file SourceFile) error
}

var sourceRules = []sourceRule{
	{"required-bounded-file", checkSourcePresent},
	{"go-ast-signatures", checkSourceGo},
	{"openapi-local-validation", checkSourceOpenAPI},
}

// CheckSourceSnapshot returns a generated receipt for the bounded registry.
// Command identifies an in-process invocation, never a shell execution.
func CheckSourceSnapshot(ctx context.Context, request CheckRequest) (*pb.CommandReceipt, error) {
	started := time.Now()
	var failures []error
	receipt := &pb.CommandReceipt{SchemaVersion: 1, ReceiptId: fmt.Sprintf("watch-%d", started.UnixNano()), Claim: WatchRegistryID, StartedAt: timestamppb.New(started), Scope: watchScope, Command: []string{WatchRegistryID}}
	for _, rule := range sourceRules {
		receipt.Command = append(receipt.Command, rule.name)
	}
	for _, file := range request.Files {
		digest := sha256.Sum256(file.Content)
		receipt.Evidence = append(receipt.Evidence, &pb.ArtifactEvidence{Path: file.Path, Sha256: hex.EncodeToString(digest[:]), Bytes: uint64(len(file.Content)), Missing: file.Missing})
		for _, rule := range sourceRules {
			if err := ctx.Err(); err != nil {
				failures = append(failures, err)
				break
			}
			if err := rule.check(ctx, file); err != nil {
				failures = append(failures, fmt.Errorf("%s %s: %w", rule.name, file.Path, err))
			}
		}
	}
	err := errors.Join(failures...)
	code := int32(0)
	if err != nil {
		code = 1
		receipt.Scope += " Findings: " + err.Error()
	}
	receipt.ExitCode = &code
	receipt.FinishedAt = timestamppb.Now()
	receipt.DurationSeconds = time.Since(started).Seconds()
	return receipt, err
}
func checkSourcePresent(ctx context.Context, file SourceFile) error {
	if file.Missing {
		return fmt.Errorf("required source is missing")
	}
	if file.Problem != "" {
		return fmt.Errorf("%s", file.Problem)
	}
	return ctx.Err()
}
func checkSourceGo(ctx context.Context, file SourceFile) error {
	if filepath.Ext(file.Path) != goExtension || file.Missing || file.Problem != "" {
		return nil
	}
	tree, err := parser.ParseFile(token.NewFileSet(), file.Path, file.Content, parser.AllErrors)
	if err != nil {
		return err
	}
	var failures []error
	ast.Inspect(tree, func(node ast.Node) bool {
		if ctx.Err() != nil {
			return false
		}
		switch item := node.(type) {
		case *ast.TypeSpec:
			if _, ok := item.Type.(*ast.InterfaceType); ok && !watchInterfaceName(item.Name.Name) {
				failures = append(failures, fmt.Errorf("interface %s lacks I prefix", item.Name.Name))
			}
		case *ast.FuncType:
			if item.Params != nil {
				for _, field := range item.Params.List {
					if len(field.Names) == 0 {
						failures = append(failures, fmt.Errorf("unnamed parameter"))
					}
				}
			}
		}
		return true
	})
	return errors.Join(append(failures, ctx.Err())...)
}
func checkSourceOpenAPI(ctx context.Context, file SourceFile) error {
	if filepath.Ext(file.Path) != yamlExtension || file.Missing || file.Problem != "" {
		return nil
	}
	loader := openapi3.NewLoader()
	loader.Context = ctx
	loader.IsExternalRefsAllowed = false
	document, err := loader.LoadFromData(file.Content)
	if err != nil {
		return err
	}
	return document.Validate(ctx)
}

func watchInterfaceName(name string) bool {
	letters := []rune(name)
	return len(letters) > 1 && (letters[0] == 'I' || letters[0] == 'i') && unicode.IsUpper(letters[1])
}
