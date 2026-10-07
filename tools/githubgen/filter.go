// Copyright 2026 Candace Labs

// Package githubgen generates CSF's GitHub protocol client inputs from
// GitHub's own OpenAPI description: Filter cuts the description down to an
// allowlist of operations, and Tools writes the MCP tool registrations for
// the same operations, so the oapi-codegen client and the tools are two
// projections of one filtered document and cannot drift.
package githubgen

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
)

// The parts of an OpenAPI 3.0 document the filter reads.
const (
	keyPaths       = "paths"
	keyComponents  = "components"
	keySchemas     = "schemas"
	keyRef         = "$ref"
	keyOperationID = "operationId"
	keyResponses   = "responses"
	keyContent     = "content"
	keySchema      = "schema"
	keyParameters  = "parameters"
	keyExamples    = "examples"
	keyExample     = "example"
	mediaJSON      = "application/json"
	refPrefix      = "#/components/"
	resultSuffix   = "-result"
	successPrefix  = "2"
	commentPrefix  = "#"
)

// topLevel are the top-level members a filtered document keeps.
var topLevel = []string{"openapi", "info", "servers"}

// methods are the operation members of a path item.
var methods = []string{"get", "put", "post", "delete", "options", "head", "patch", "trace"}

// ErrUnknownOperation reports an allowlisted operationId the description
// does not have.
var ErrUnknownOperation = errors.New("githubgen: operation not in the description")

// ReadAllowlist reads one operationId per line; blank lines and # comments
// are skipped.
func ReadAllowlist(content []byte) []string {
	var operations []string
	scanner := bufio.NewScanner(bytes.NewReader(content))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line != "" && !strings.HasPrefix(line, commentPrefix) {
			operations = append(operations, line)
		}
	}
	return operations
}

// ResultSchemaName is the component an operation's inline success schema is
// hoisted into.
func ResultSchemaName(operationID string) string {
	return strings.ReplaceAll(operationID, "/", "-") + resultSuffix
}

// Filter keeps the allowlisted operations of an OpenAPI 3.0 description,
// their success responses only, and the components they reach; examples are
// dropped. An inline JSON success schema is hoisted into
// components/schemas/<operation>-result so every success type has a name.
// The output is indented JSON with sorted keys: the same input always
// yields the same bytes.
func Filter(description []byte, operations []string) ([]byte, error) {
	var document map[string]any
	if err := json.Unmarshal(description, &document); err != nil {
		return nil, fmt.Errorf("githubgen: decode the description: %w", err)
	}
	wanted := map[string]bool{}
	for _, operation := range operations {
		wanted[operation] = true
	}
	components, _ := document[keyComponents].(map[string]any)
	hoisted := map[string]any{}
	paths := map[string]any{}
	for path, raw := range asMap(document[keyPaths]) {
		item := asMap(raw)
		kept := map[string]any{}
		for _, method := range methods {
			operation := asMap(item[method])
			identifier, _ := operation[keyOperationID].(string)
			if !wanted[identifier] {
				continue
			}
			delete(wanted, identifier)
			operation[keyResponses] = successResponses(identifier, asMap(operation[keyResponses]), hoisted)
			kept[method] = operation
		}
		if len(kept) == 0 {
			continue
		}
		if parameters, ok := item[keyParameters]; ok {
			kept[keyParameters] = parameters
		}
		paths[path] = kept
	}
	if len(wanted) > 0 {
		missing := make([]string, 0, len(wanted))
		for identifier := range wanted {
			missing = append(missing, identifier)
		}
		sort.Strings(missing)
		return nil, fmt.Errorf("%w: %s", ErrUnknownOperation, strings.Join(missing, ", "))
	}
	filtered := map[string]any{keyPaths: paths}
	for _, key := range topLevel {
		if value, ok := document[key]; ok {
			filtered[key] = value
		}
	}
	reached := map[string]any{keySchemas: hoisted}
	pending := references(paths, nil)
	pending = references(hoisted, pending)
	for len(pending) > 0 {
		ref := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		section, name, ok := strings.Cut(strings.TrimPrefix(ref, refPrefix), "/")
		if !ok {
			return nil, fmt.Errorf("githubgen: unsupported reference %q", ref)
		}
		bucket := asMap(reached[section])
		if _, done := bucket[name]; done {
			continue
		}
		target, found := asMap(components[section])[name]
		if !found {
			return nil, fmt.Errorf("githubgen: dangling reference %q", ref)
		}
		bucket[name] = target
		reached[section] = bucket
		pending = references(target, pending)
	}
	filtered[keyComponents] = reached
	stripExamples(filtered)
	return json.MarshalIndent(filtered, "", "  ")
}

// successResponses keeps an operation's 2xx responses and hoists an inline
// JSON success schema into hoisted.
func successResponses(operation string, responses map[string]any, hoisted map[string]any) map[string]any {
	kept := map[string]any{}
	for status, raw := range responses {
		if !strings.HasPrefix(status, successPrefix) {
			continue
		}
		response := asMap(raw)
		media := asMap(asMap(response[keyContent])[mediaJSON])
		if schema, ok := media[keySchema].(map[string]any); ok {
			if _, named := schema[keyRef]; !named {
				name := ResultSchemaName(operation)
				hoisted[name] = schema
				media[keySchema] = map[string]any{keyRef: refPrefix + keySchemas + "/" + name}
			}
		}
		kept[status] = response
	}
	return kept
}

// references appends every $ref under node to found.
func references(node any, found []string) []string {
	switch typed := node.(type) {
	case map[string]any:
		if ref, ok := typed[keyRef].(string); ok {
			found = append(found, ref)
		}
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			found = references(typed[key], found)
		}
	case []any:
		for _, element := range typed {
			found = references(element, found)
		}
	}
	return found
}

// stripExamples removes example and examples members everywhere but inside
// a schema's properties, where they would be property names.
func stripExamples(node any) {
	switch typed := node.(type) {
	case map[string]any:
		for key, value := range typed {
			if key == "properties" {
				for _, property := range asMap(value) {
					stripExamples(property)
				}
				continue
			}
			if key == keyExample || key == keyExamples {
				delete(typed, key)
				continue
			}
			stripExamples(value)
		}
	case []any:
		for _, element := range typed {
			stripExamples(element)
		}
	}
}

// asMap is node as a JSON object; an absent or non-object node is empty.
func asMap(node any) map[string]any {
	typed, ok := node.(map[string]any)
	if !ok {
		return map[string]any{}
	}
	return typed
}

// Operations lists the operationIds a filtered document holds, sorted.
func Operations(filtered []byte) ([]string, error) {
	var document map[string]any
	if err := json.Unmarshal(filtered, &document); err != nil {
		return nil, fmt.Errorf("githubgen: decode the filtered description: %w", err)
	}
	var identifiers []string
	for _, raw := range asMap(document[keyPaths]) {
		for _, method := range methods {
			if identifier, ok := asMap(asMap(raw)[method])[keyOperationID].(string); ok {
				identifiers = append(identifiers, identifier)
			}
		}
	}
	slices.Sort(identifiers)
	return identifiers, nil
}
