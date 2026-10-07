// Copyright 2026 Candace Labs

package githubgen

import (
	"bytes"
	"encoding/json"
	"fmt"
	"go/format"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"text/template"
	"unicode"
)

const (
	keySummary     = "summary"
	keyDescription = "description"
	keyName        = "name"
	keyIn          = "in"
	keyType        = "type"
	keyFormat      = "format"
	keyRequestBody = "requestBody"
	inPath         = "path"
	inQuery        = "query"
	typeInteger    = "integer"
	typeArray      = "array"
	typeBoolean    = "boolean"
	formatInt64    = "int64"
	goString       = "string"
	goInt          = "int"
	goInt64        = "int64"
	goBool         = "bool"
)

// pathParameter matches one {name} of a path template.
var pathParameter = regexp.MustCompile(`\{([^}]+)\}`)

// toolOperation is one operation as the tools template reads it.
type toolOperation struct {
	ID          string
	Tool        string
	Client      string
	Description string
	PathFields  []field
	NumberField string
	Params      bool
	Body        bool
	Status      string
	Array       bool
}

type field struct {
	Name        string
	Type        string
	JSON        string
	Description string
}

// Tools writes csf/githubtools/tools.gen.go for a filtered description: per
// operation an input type, a handler calling the oapi-codegen client and
// returning its typed success body, and the operation's registration.
func Tools(filtered []byte) ([]byte, error) {
	var document map[string]any
	if err := json.Unmarshal(filtered, &document); err != nil {
		return nil, fmt.Errorf("githubgen: decode the filtered description: %w", err)
	}
	components := asMap(document[keyComponents])
	var operations []toolOperation
	for path, raw := range asMap(document[keyPaths]) {
		item := asMap(raw)
		for _, method := range methods {
			operation := asMap(item[method])
			identifier, ok := operation[keyOperationID].(string)
			if !ok {
				continue
			}
			parsed, err := toolOperationOf(identifier, path, operation, item, components)
			if err != nil {
				return nil, err
			}
			operations = append(operations, parsed)
		}
	}
	sort.Slice(operations, func(left int, right int) bool { return operations[left].ID < operations[right].ID })
	var output bytes.Buffer
	if err := toolsTemplate.Execute(&output, operations); err != nil {
		return nil, fmt.Errorf("githubgen: render the tools: %w", err)
	}
	formatted, err := format.Source(output.Bytes())
	if err != nil {
		return nil, fmt.Errorf("githubgen: format the tools: %w\n%s", err, output.String())
	}
	return formatted, nil
}

func toolOperationOf(identifier string, path string, operation map[string]any, item map[string]any, components map[string]any) (toolOperation, error) {
	parsed := toolOperation{ID: identifier, Tool: ToolName(identifier), Client: ClientName(identifier), Description: describe(identifier, operation)}
	parameters := map[string]map[string]any{}
	for _, raw := range append(asSlice(item[keyParameters]), asSlice(operation[keyParameters])...) {
		parameter := resolve(asMap(raw), components)
		name, _ := parameter[keyName].(string)
		switch parameter[keyIn] {
		case inPath:
			parameters[name] = parameter
		case inQuery:
			parsed.Params = true
		}
	}
	for _, match := range pathParameter.FindAllStringSubmatch(path, -1) {
		parameter, ok := parameters[match[1]]
		if !ok {
			return toolOperation{}, fmt.Errorf("githubgen: %s: path parameter %s is not declared", identifier, match[1])
		}
		goType, err := goTypeOf(resolve(asMap(parameter[keySchema]), components))
		if err != nil {
			return toolOperation{}, fmt.Errorf("githubgen: %s: parameter %s: %w", identifier, match[1], err)
		}
		description, _ := parameter[keyDescription].(string)
		pathField := field{Name: camel(match[1], true), Type: goType, JSON: match[1], Description: firstSentence(description)}
		parsed.PathFields = append(parsed.PathFields, pathField)
		if goType == goInt && strings.HasSuffix(match[1], "_number") {
			parsed.NumberField = pathField.Name
		}
	}
	if body := asMap(operation[keyRequestBody]); len(body) > 0 {
		if _, ok := asMap(asMap(body[keyContent])[mediaJSON])[keySchema]; ok {
			parsed.Body = true
		}
	}
	statuses := make([]string, 0)
	for status := range asMap(operation[keyResponses]) {
		statuses = append(statuses, status)
	}
	sort.Strings(statuses)
	for _, status := range statuses {
		schema := asMap(asMap(asMap(asMap(asMap(operation[keyResponses])[status])[keyContent])[mediaJSON])[keySchema])
		ref, ok := schema[keyRef].(string)
		if !ok {
			continue
		}
		name := strings.TrimPrefix(ref, refPrefix+keySchemas+"/")
		parsed.Status = status
		parsed.Array = asMap(asMap(components[keySchemas])[name])[keyType] == typeArray
		return parsed, nil
	}
	return toolOperation{}, fmt.Errorf("githubgen: %s has no named JSON success response", identifier)
}

// ToolName is the MCP tool an operationId is served as: each word of it
// capitalized, issues/create-comment as IssuesCreateComment.
func ToolName(operationID string) string { return camel(operationID, true) }

// ClientName is oapi-codegen's name for an operationId's client method
// stem: the first letter capitalized, a slash dropped, a dash capitalizing
// the next letter, issues/create-comment as IssuescreateComment.
func ClientName(operationID string) string {
	var name strings.Builder
	upper := true
	for _, character := range operationID {
		switch {
		case character == '-' || character == '_':
			upper = true
		case character == '/':
		case upper:
			name.WriteRune(unicode.ToUpper(character))
			upper = false
		default:
			name.WriteRune(character)
		}
	}
	return name.String()
}

func camel(text string, upperFirst bool) string {
	var name strings.Builder
	upper := upperFirst
	for _, character := range text {
		switch {
		case character == '-' || character == '_' || character == '/':
			upper = true
		case upper:
			name.WriteRune(unicode.ToUpper(character))
			upper = false
		default:
			name.WriteRune(character)
		}
	}
	return name.String()
}

func goTypeOf(schema map[string]any) (string, error) {
	switch schema[keyType] {
	case goString:
		return goString, nil
	case typeInteger:
		if schema[keyFormat] == formatInt64 {
			return goInt64, nil
		}
		return goInt, nil
	case typeBoolean:
		return goBool, nil
	}
	return "", fmt.Errorf("unsupported parameter schema %v", schema)
}

// resolve follows one $ref into components.
func resolve(node map[string]any, components map[string]any) map[string]any {
	ref, ok := node[keyRef].(string)
	if !ok {
		return node
	}
	section, name, _ := strings.Cut(strings.TrimPrefix(ref, refPrefix), "/")
	return asMap(asMap(components[section])[name])
}

func asSlice(node any) []any {
	typed, _ := node.([]any)
	return typed
}

// describe is the tool description: the operation's summary and the first
// paragraph of its description, then the operation it is.
func describe(identifier string, operation map[string]any) string {
	summary, _ := operation[keySummary].(string)
	description, _ := operation[keyDescription].(string)
	paragraph, _, _ := strings.Cut(strings.TrimSpace(description), "\n\n")
	var sentences []string
	for _, part := range []string{summary, strings.Join(strings.Fields(paragraph), " ")} {
		if part = strings.TrimSuffix(strings.TrimSpace(part), "."); part != "" {
			sentences = append(sentences, part+".")
		}
	}
	return strings.Join(append(sentences, "GitHub REST operation "+identifier+"."), " ")
}

func firstSentence(text string) string {
	sentence, _, _ := strings.Cut(strings.Join(strings.Fields(text), " "), ". ")
	// A struct tag cannot hold a backquote.
	return strings.ReplaceAll(strings.TrimSuffix(sentence, "."), "`", "'")
}

var toolsTemplate = template.Must(template.New("tools").Funcs(template.FuncMap{"quote": strconv.Quote}).Parse(`// Code generated by tools/githubgen from ipc/github/api.github.com.json. DO NOT EDIT.

package githubtools

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/candacelabs/csf/csf"
	"github.com/candacelabs/csf/io/net/github"
)

// The generated GitHub tools' names.
const (
{{- range .}}
	Tool{{.Tool}} = {{quote .Tool}}
{{- end}}
)
{{range .}}
// {{.Tool}}Input is the input of the {{.Tool}} tool.
type {{.Tool}}Input struct {
{{- range .PathFields}}
	{{.Name}} {{.Type}} ` + "`" + `json:"{{.JSON}}" jsonschema:{{quote .Description}}` + "`" + `
{{- end}}
{{- if .Params}}
	Params *github.{{.Client}}Params ` + "`" + `json:"params,omitempty" jsonschema:"query parameters"` + "`" + `
{{- end}}
{{- if .Body}}
	Body github.{{.Client}}JSONRequestBody ` + "`" + `json:"body" jsonschema:"the request body"` + "`" + `
{{- end}}
}

func (tools *GitHubTools) handle{{.Tool}}(ctx context.Context, input {{.Tool}}Input) ({{if .Array}}ListOutput{{else}}json.RawMessage{{end}}, error) {
	call := toolCall{operation: Tool{{.Tool}}, owner: input.Owner, repo: input.Repo{{if .NumberField}}, number: input.{{.NumberField}}{{end}}}
	if err := tools.prepare(ctx, &call, &input); err != nil {
		return {{if .Array}}ListOutput{}{{else}}nil{{end}}, err
	}
	response, err := tools.client.{{.Client}}WithResponse(ctx{{range .PathFields}}, input.{{.Name}}{{end}}{{if .Params}}, input.Params{{end}}{{if .Body}}, input.Body{{end}})
	if err != nil {
		return {{if .Array}}listed({{end}}finish(ctx, tools, call, nil, nil, false, err){{if .Array}}){{end}}
	}
	return {{if .Array}}listed({{end}}finish(ctx, tools, call, response.HTTPResponse, response.Body, response.JSON{{.Status}} != nil, nil){{if .Array}}){{end}}
}
{{end}}
// generatedOperations are the operationIds the generated tools serve.
var generatedOperations = []string{
{{- range .}}
	{{quote .ID}},
{{- end}}
}

// generated is every generated GitHub tool, each served at RoutePrefix plus
// its operationId.
func (tools *GitHubTools) generated() []csf.Operation {
	return []csf.Operation{
{{- range .}}
		csf.NewToolOperation(tools.tool(Tool{{.Tool}}, {{quote .ID}}, {{quote .Description}}), http.MethodPost, RoutePrefix+{{quote .ID}}, tools.handle{{.Tool}}),
{{- end}}
	}
}
`))
