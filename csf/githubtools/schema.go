// Copyright 2026 Candace Labs

package githubtools

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// An OpenAPI 3.0 description's members, and the JSON Schema 2020-12 members
// a tool schema is written in.
const (
	memberPaths       = "paths"
	memberComponents  = "components"
	memberSchemas     = "schemas"
	memberParameters  = "parameters"
	memberOperationID = "operationId"
	memberRequestBody = "requestBody"
	memberResponses   = "responses"
	memberContent     = "content"
	memberSchema      = "schema"
	memberRequired    = "required"
	memberRef         = "$ref"
	memberDefs        = "$defs"
	memberNullable    = "nullable"
	memberType        = "type"
	memberProperties  = "properties"
	memberAnyOf       = "anyOf"
	memberName        = "name"
	memberIn          = "in"
	memberDescription = "description"
	mediaJSON         = "application/json"
	componentPrefix   = "#/components/"
	schemaPrefix      = "#/components/schemas/"
	defsPrefix        = "#/$defs/"
	extensionPrefix   = "x-"
	inPath            = "path"
	inQuery           = "query"
	typeObject        = "object"
	typeArray         = "array"
	typeNull          = "null"
	fieldParams       = "params"
	fieldBody         = "body"
	fieldItems        = "items"
	successPrefix     = "2"
)

// dropped are OpenAPI 3.0 members JSON Schema 2020-12 does not have.
var dropped = map[string]bool{memberNullable: true, "discriminator": true, "xml": true, "externalDocs": true}

// methods are the operation members of a path item.
var methods = []string{"get", "put", "post", "delete", "patch"}

// toolSchemas is one operation's tool input and output JSON Schemas.
type toolSchemas struct {
	input  map[string]any
	output map[string]any
}

// schemasOf builds every operation's tool schemas from an OpenAPI 3.0
// description: the input is an object of the path parameters, params (the
// query parameters) and body (the JSON request body), the output the first
// JSON success response, an array wrapped as {"items": [...]} because an MCP
// result is an object. Component schemas become $defs and nullable becomes
// a type that admits null.
func schemasOf(description []byte) (map[string]toolSchemas, error) {
	var document map[string]any
	if err := json.Unmarshal(description, &document); err != nil {
		return nil, fmt.Errorf("github tools: decode the description: %w", err)
	}
	components := object(document[memberComponents])
	schemas := map[string]toolSchemas{}
	for _, raw := range object(document[memberPaths]) {
		item := object(raw)
		for _, method := range methods {
			operation := object(item[method])
			identifier, ok := operation[memberOperationID].(string)
			if !ok {
				continue
			}
			built, err := operationSchemas(identifier, operation, item, components)
			if err != nil {
				return nil, err
			}
			schemas[identifier] = built
		}
	}
	return schemas, nil
}

func operationSchemas(identifier string, operation map[string]any, item map[string]any, components map[string]any) (toolSchemas, error) {
	properties := map[string]any{}
	required := []any{}
	query := map[string]any{}
	queryRequired := []any{}
	parameters := append(list(item[memberParameters]), list(operation[memberParameters])...)
	for _, raw := range parameters {
		parameter := object(raw)
		if ref, ok := parameter[memberRef].(string); ok {
			section, name, _ := strings.Cut(strings.TrimPrefix(ref, componentPrefix), "/")
			parameter = object(object(components[section])[name])
		}
		name, _ := parameter[memberName].(string)
		schema := object(convert(parameter[memberSchema]))
		if description, ok := parameter[memberDescription].(string); ok {
			schema[memberDescription] = description
		}
		isRequired, _ := parameter[memberRequired].(bool)
		switch parameter[memberIn] {
		case inPath:
			properties[name] = schema
			required = append(required, name)
		case inQuery:
			query[name] = schema
			if isRequired {
				queryRequired = append(queryRequired, name)
			}
		}
	}
	if len(query) > 0 {
		params := map[string]any{memberType: typeObject, memberProperties: query}
		if len(queryRequired) > 0 {
			params[memberRequired] = queryRequired
		}
		properties[fieldParams] = params
	}
	body := object(operation[memberRequestBody])
	if schema, ok := object(object(body[memberContent])[mediaJSON])[memberSchema]; ok {
		properties[fieldBody] = convert(schema)
		if isRequired, _ := body[memberRequired].(bool); isRequired {
			required = append(required, fieldBody)
		}
	}
	input := map[string]any{memberType: typeObject, memberProperties: properties, memberRequired: required}
	output, err := successSchema(identifier, object(operation[memberResponses]), components)
	if err != nil {
		return toolSchemas{}, err
	}
	if err := withDefinitions(input, components); err != nil {
		return toolSchemas{}, fmt.Errorf("github tools: %s input: %w", identifier, err)
	}
	if err := withDefinitions(output, components); err != nil {
		return toolSchemas{}, fmt.Errorf("github tools: %s output: %w", identifier, err)
	}
	return toolSchemas{input: input, output: output}, nil
}

// successSchema is the first JSON success response's schema, an array
// wrapped in an object.
func successSchema(identifier string, responses map[string]any, components map[string]any) (map[string]any, error) {
	statuses := make([]string, 0, len(responses))
	for status := range responses {
		statuses = append(statuses, status)
	}
	sort.Strings(statuses)
	for _, status := range statuses {
		if !strings.HasPrefix(status, successPrefix) {
			continue
		}
		schema, ok := object(object(object(object(responses[status])[memberContent])[mediaJSON]))[memberSchema]
		if !ok {
			continue
		}
		converted := object(convert(schema))
		target := converted
		if ref, ok := converted[memberRef].(string); ok {
			target = object(object(components[memberSchemas])[strings.TrimPrefix(ref, defsPrefix)])
		}
		if target[memberType] == typeArray {
			return map[string]any{memberType: typeObject, memberProperties: map[string]any{fieldItems: converted}, memberRequired: []any{fieldItems}}, nil
		}
		return converted, nil
	}
	return nil, fmt.Errorf("github tools: %s has no JSON success response", identifier)
}

// withDefinitions adds every component schema root reaches as $defs.
func withDefinitions(root map[string]any, components map[string]any) error {
	definitions := map[string]any{}
	pending := refs(root, nil)
	for len(pending) > 0 {
		name := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		if _, done := definitions[name]; done {
			continue
		}
		source, ok := object(components[memberSchemas])[name]
		if !ok {
			return fmt.Errorf("dangling reference to %s", name)
		}
		converted := convert(source)
		definitions[name] = converted
		pending = refs(converted, pending)
	}
	if len(definitions) > 0 {
		root[memberDefs] = definitions
	}
	return nil
}

// refs appends the $defs names node refers to.
func refs(node any, found []string) []string {
	switch typed := node.(type) {
	case map[string]any:
		if ref, ok := typed[memberRef].(string); ok {
			found = append(found, strings.TrimPrefix(ref, defsPrefix))
		}
		for key, value := range typed {
			if key != memberDefs {
				found = refs(value, found)
			}
		}
	case []any:
		for _, element := range typed {
			found = refs(element, found)
		}
	}
	return found
}

// convert copies an OpenAPI 3.0 schema as JSON Schema 2020-12: component
// references point into $defs, nullable admits null, and members JSON
// Schema lacks are dropped.
func convert(node any) any {
	switch typed := node.(type) {
	case map[string]any:
		converted := map[string]any{}
		for key, value := range typed {
			switch {
			case dropped[key] || strings.HasPrefix(key, extensionPrefix):
			case key == memberRef:
				ref, _ := value.(string)
				converted[key] = defsPrefix + strings.TrimPrefix(ref, schemaPrefix)
			case key == memberProperties:
				properties := map[string]any{}
				for name, property := range object(value) {
					properties[name] = convert(property)
				}
				converted[key] = properties
			default:
				converted[key] = convert(value)
			}
		}
		if nullable, _ := typed[memberNullable].(bool); nullable {
			return admitNull(converted)
		}
		return converted
	case []any:
		converted := make([]any, 0, len(typed))
		for _, element := range typed {
			converted = append(converted, convert(element))
		}
		return converted
	}
	return node
}

// admitNull widens a schema to admit null: a typed schema gains the null
// type, anything else becomes anyOf it or null.
func admitNull(schema map[string]any) map[string]any {
	if kind, ok := schema[memberType].(string); ok {
		schema[memberType] = []any{kind, typeNull}
		if values, ok := schema["enum"].([]any); ok {
			schema["enum"] = append(values, nil)
		}
		return schema
	}
	return map[string]any{memberAnyOf: []any{schema, map[string]any{memberType: typeNull}}}
}

func object(node any) map[string]any {
	typed, ok := node.(map[string]any)
	if !ok {
		return map[string]any{}
	}
	return typed
}

func list(node any) []any {
	typed, _ := node.([]any)
	return typed
}
