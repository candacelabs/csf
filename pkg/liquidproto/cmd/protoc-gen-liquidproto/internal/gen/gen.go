// Package gen turns Liquid Proto field refinements into Go validators.
package gen

import (
	"fmt"
	"strconv"

	"github.com/candacelabs/csf/pkg/liquidproto/cmd/protoc-gen-liquidproto/internal/expr"
	liquidv1 "github.com/candacelabs/csf/pkg/liquidproto/v1"
	"google.golang.org/protobuf/compiler/protogen"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/gofeaturespb"
)

const Version = "v0.1.0"

var (
	runtimePackage = protogen.GoImportPath("github.com/candacelabs/csf/pkg/liquidproto")
	regexpPackage  = protogen.GoImportPath("regexp")
	fmtPackage     = protogen.GoImportPath("fmt")
	sortPackage    = protogen.GoImportPath("sort")
)

type validatedField struct {
	field           *protogen.Field
	program         *expr.Program
	mapKeyProgram   *expr.Program
	mapValueProgram *expr.Program
	nestedMessage   *protogen.Message
}

type validatedMessage struct {
	message *protogen.Message
	fields  []validatedField
}

// Run emits <source>_liquid.pb.go for requested files with refinements.
func Run(plugin *protogen.Plugin) error {
	for _, file := range plugin.Files {
		if !file.Generate {
			continue
		}
		messages, err := collect(file)
		if err != nil {
			return err
		}
		if len(messages) == 0 {
			continue
		}
		if file.APILevel != gofeaturespb.GoFeatures_API_OPEN {
			return fmt.Errorf(
				"%s: refinements require the open Go protobuf API, got %v",
				file.Desc.Path(),
				file.APILevel,
			)
		}
		emit(plugin, file, messages)
	}
	return nil
}

func collect(file *protogen.File) ([]validatedMessage, error) {
	if err := validateNestedValidationCycles(file); err != nil {
		return nil, err
	}
	var output []validatedMessage
	var walk func(messages []*protogen.Message) error
	walk = func(messages []*protogen.Message) error {
		for _, message := range messages {
			if message.Desc.IsMapEntry() {
				continue
			}
			if err := rejectExtensions(message.Extensions); err != nil {
				return err
			}
			validated := validatedMessage{message: message}
			for _, field := range message.Fields {
				annotation, err := refinementOf(field)
				if err != nil {
					return err
				}
				if annotation == nil {
					continue
				}
				compiled, err := compileField(message, field, annotation)
				if err != nil {
					return err
				}
				validated.fields = append(validated.fields, compiled)
			}
			if len(validated.fields) > 0 {
				output = append(output, validated)
			}
			if err := walk(message.Messages); err != nil {
				return err
			}
		}
		return nil
	}
	if err := walk(file.Messages); err != nil {
		return nil, err
	}
	if err := rejectExtensions(file.Extensions); err != nil {
		return nil, err
	}
	return output, nil
}

func compileField(
	message *protogen.Message,
	field *protogen.Field,
	annotation *liquidv1.FieldRefinement,
) (validatedField, error) {
	if annotation.GetRecurse() {
		return compileNestedMessageField(field, annotation)
	}
	if field.Desc.IsMap() {
		return compileMapField(message, field, annotation)
	}
	if annotation.GetMapKeyExpr() != "" || annotation.GetMapValueExpr() != "" {
		return validatedField{}, fieldErrf(field, "map entry predicates require a map field")
	}
	if field.Desc.IsList() {
		return validatedField{}, fieldErrf(field, "repeated fields cannot be refined")
	}
	base, ok := expr.FromProtoKind(field.Desc.Kind())
	if !ok {
		return validatedField{}, fieldErrf(field, "%s fields cannot be refined", field.Desc.Kind().String())
	}
	if field.Oneof != nil && !field.Oneof.Desc.IsSynthetic() {
		return validatedField{}, fieldErrf(field, "fields inside oneof %s cannot be refined", field.Oneof.Desc.Name())
	}
	if field.Desc.HasPresence() {
		return validatedField{}, fieldErrf(field, "fields with explicit presence cannot be refined")
	}
	if annotation.GetExpr() == "" {
		return validatedField{}, fieldErrf(field, "refinement has no expr")
	}
	program, err := expr.Compile(
		annotation.GetExpr(),
		base,
		"message."+field.GoName,
		"_liquid"+message.GoIdent.GoName+field.GoName+"Re",
	)
	if err != nil {
		return validatedField{}, fieldErrf(field, "%v", err)
	}
	return validatedField{field: field, program: program}, nil
}

func compileNestedMessageField(field *protogen.Field, annotation *liquidv1.FieldRefinement) (validatedField, error) {
	if annotation.GetExpr() != "" || annotation.GetMapKeyExpr() != "" || annotation.GetMapValueExpr() != "" {
		return validatedField{}, fieldErrf(field, "nested message validation cannot be combined with predicates")
	}
	if field.Desc.IsMap() || field.Desc.IsList() || field.Desc.Kind() != protoreflect.MessageKind || field.Message == nil {
		return validatedField{}, fieldErrf(field, "nested validation requires a singular message field")
	}
	if field.Oneof != nil && !field.Oneof.Desc.IsSynthetic() {
		return validatedField{}, fieldErrf(field, "fields inside oneof %s cannot use nested validation", field.Oneof.Desc.Name())
	}
	hasValidator, err := hasGeneratedValidator(field.Message)
	if err != nil {
		return validatedField{}, err
	}
	if !hasValidator {
		return validatedField{}, fieldErrf(field, "nested validation target %s has no Liquid Proto validator", field.Message.Desc.FullName())
	}
	return validatedField{field: field, nestedMessage: field.Message}, nil
}

func compileMapField(
	message *protogen.Message,
	field *protogen.Field,
	annotation *liquidv1.FieldRefinement,
) (validatedField, error) {
	if annotation.GetExpr() != "" {
		return validatedField{}, fieldErrf(field, "map refinements use map_key_expr and map_value_expr")
	}
	if annotation.GetMapKeyExpr() == "" && annotation.GetMapValueExpr() == "" {
		return validatedField{}, fieldErrf(field, "map refinement has no key or value predicate")
	}
	if field.Desc.MapKey().Kind() != protoreflect.StringKind {
		return validatedField{}, fieldErrf(field, "map refinements require string keys")
	}
	valueType, ok := expr.FromProtoKind(field.Desc.MapValue().Kind())
	if !ok {
		return validatedField{}, fieldErrf(field, "%s map values cannot be refined", field.Desc.MapValue().Kind())
	}

	prefix := "_liquid" + message.GoIdent.GoName + field.GoName
	validated := validatedField{field: field}
	var err error
	if annotation.GetMapKeyExpr() != "" {
		keyType, _ := expr.FromProtoKind(field.Desc.MapKey().Kind())
		validated.mapKeyProgram, err = expr.Compile(
			annotation.GetMapKeyExpr(),
			keyType,
			prefix+"Key",
			prefix+"KeyRe",
		)
		if err != nil {
			return validatedField{}, fieldErrf(field, "map key: %v", err)
		}
	}
	if annotation.GetMapValueExpr() != "" {
		validated.mapValueProgram, err = expr.Compile(
			annotation.GetMapValueExpr(),
			valueType,
			prefix+"Value",
			prefix+"ValueRe",
		)
		if err != nil {
			return validatedField{}, fieldErrf(field, "map value: %v", err)
		}
	}
	return validated, nil
}

func rejectExtensions(extensions []*protogen.Field) error {
	for _, extension := range extensions {
		annotation, err := refinementOf(extension)
		if err != nil {
			return err
		}
		if annotation != nil {
			return fieldErrf(extension, "extension fields cannot be refined")
		}
	}
	return nil
}

type nestedVisit uint8

const (
	nestedVisitVisiting nestedVisit = iota + 1
	nestedVisitComplete
)

func validateNestedValidationCycles(file *protogen.File) error {
	states := make(map[protoreflect.FullName]nestedVisit)
	var visit func(message *protogen.Message) error
	visit = func(message *protogen.Message) error {
		if message == nil || message.Desc.IsMapEntry() {
			return nil
		}
		name := message.Desc.FullName()
		switch states[name] {
		case nestedVisitComplete:
			return nil
		case nestedVisitVisiting:
			return fmt.Errorf("nested validation cycle through message %s", name)
		}
		states[name] = nestedVisitVisiting
		for _, field := range message.Fields {
			annotation, err := refinementOf(field)
			if err != nil {
				return err
			}
			if annotation == nil || !annotation.GetRecurse() || field.Message == nil {
				continue
			}
			if states[field.Message.Desc.FullName()] == nestedVisitVisiting {
				return fieldErrf(field, "nested validation forms a cycle through message %s", field.Message.Desc.FullName())
			}
			if err := visit(field.Message); err != nil {
				return err
			}
		}
		states[name] = nestedVisitComplete
		return nil
	}
	var visitMessages func(messages []*protogen.Message) error
	visitMessages = func(messages []*protogen.Message) error {
		for _, message := range messages {
			if err := visit(message); err != nil {
				return err
			}
			if err := visitMessages(message.Messages); err != nil {
				return err
			}
		}
		return nil
	}
	return visitMessages(file.Messages)
}

func hasGeneratedValidator(message *protogen.Message) (bool, error) {
	for _, field := range message.Fields {
		annotation, err := refinementOf(field)
		if err != nil {
			return false, err
		}
		if annotation != nil {
			return true, nil
		}
	}
	return false, nil
}

func fieldErrf(field *protogen.Field, format string, args ...any) error {
	where := string(field.Desc.FullName())
	if field.Parent != nil {
		where = fmt.Sprintf("message %s: field %s", field.Parent.Desc.FullName(), field.Desc.Name())
	}
	return fmt.Errorf("%s: %s: %s", field.Desc.ParentFile().Path(), where, fmt.Sprintf(format, args...))
}

func refinementOf(field *protogen.Field) (*liquidv1.FieldRefinement, error) {
	options, ok := field.Desc.Options().(*descriptorpb.FieldOptions)
	if !ok || options == nil {
		return nil, nil
	}
	if proto.HasExtension(options, liquidv1.E_Field) {
		annotation, ok := proto.GetExtension(options, liquidv1.E_Field).(*liquidv1.FieldRefinement)
		if !ok {
			return nil, fieldErrf(field, "refinement option has unexpected Go type")
		}
		return annotation, nil
	}
	return nil, nil
}

func emit(plugin *protogen.Plugin, file *protogen.File, messages []validatedMessage) {
	generated := plugin.NewGeneratedFile(file.GeneratedFilenamePrefix+"_liquid.pb.go", file.GoImportPath)
	generated.P("// Code generated by protoc-gen-liquidproto. DO NOT EDIT.")
	generated.P("// versions:")
	generated.P("// \tprotoc-gen-liquidproto ", Version)
	if version := plugin.Request.GetCompilerVersion(); version != nil {
		generated.P("// \tprotoc                 ", fmt.Sprintf(
			"v%d.%d.%d%s",
			version.GetMajor(),
			version.GetMinor(),
			version.GetPatch(),
			version.GetSuffix(),
		))
	}
	generated.P("// source: ", file.Desc.Path())
	generated.P()
	generated.P("package ", file.GoPackageName)
	generated.P()

	emitRegexps(generated, messages)
	for _, message := range messages {
		emitValidator(generated, message)
	}
}

func emitRegexps(generated *protogen.GeneratedFile, messages []validatedMessage) {
	var regexps []expr.Regexp
	for _, message := range messages {
		for _, field := range message.fields {
			for _, program := range []*expr.Program{field.program, field.mapKeyProgram, field.mapValueProgram} {
				if program != nil {
					regexps = append(regexps, program.Regexps...)
				}
			}
		}
	}
	if len(regexps) == 0 {
		return
	}
	generated.P("var (")
	for _, compiled := range regexps {
		generated.P(
			compiled.VarName,
			" = ",
			generated.QualifiedGoIdent(regexpPackage.Ident("MustCompile")),
			"(",
			strconv.Quote(compiled.Pattern),
			")",
		)
	}
	generated.P(")")
	generated.P()
}

func emitValidator(generated *protogen.GeneratedFile, validated validatedMessage) {
	message := validated.message
	name := message.GoIdent.GoName
	if hasNestedMessageValidation(validated) {
		generated.P("// Validate", name, " checks this message's annotated fields and opted-in nested messages.")
	} else {
		generated.P("// Validate", name, " checks this message's annotated fields; it does not recurse.")
	}
	generated.P("// A failed predicate returns *liquidproto.Error. Nil input also returns an error.")
	generated.P("func Validate", name, "(message *", name, ") error {")
	generated.P("if message == nil {")
	generated.P("return ", generated.QualifiedGoIdent(fmtPackage.Ident("Errorf")), "(\"Validate", name, ": nil *", name, "\")")
	generated.P("}")
	for _, validatedField := range validated.fields {
		if validatedField.nestedMessage != nil {
			emitNestedMessageValidation(generated, validatedField)
			continue
		}
		if validatedField.field.Desc.IsMap() {
			emitMapValidation(generated, message, validatedField)
			continue
		}
		field := validatedField.field
		program := validatedField.program
		emitPredicate(generated, message, field, program, "message."+field.GoName)
	}
	generated.P("return nil")
	generated.P("}")
	generated.P()
}

func hasNestedMessageValidation(validated validatedMessage) bool {
	for _, field := range validated.fields {
		if field.nestedMessage != nil {
			return true
		}
	}
	return false
}

func emitNestedMessageValidation(generated *protogen.GeneratedFile, validated validatedField) {
	value := "message." + validated.field.GoName
	validator := protogen.GoIdent{
		GoName:       "Validate" + validated.nestedMessage.GoIdent.GoName,
		GoImportPath: validated.nestedMessage.GoIdent.GoImportPath,
	}
	generated.P("if ", value, " != nil {")
	generated.P("if err := ", generated.QualifiedGoIdent(validator), "(", value, "); err != nil {")
	generated.P("return err")
	generated.P("}")
	generated.P("}")
}

func emitMapValidation(generated *protogen.GeneratedFile, message *protogen.Message, validated validatedField) {
	field := validated.field
	prefix := "_liquid" + message.GoIdent.GoName + field.GoName
	keysName := prefix + "Keys"
	keyName := prefix + "Key"
	valueName := prefix + "Value"
	generated.P(keysName, " := make([]string, 0, len(message.", field.GoName, "))")
	generated.P("for ", keyName, " := range message.", field.GoName, " {")
	generated.P(keysName, " = append(", keysName, ", ", keyName, ")")
	generated.P("}")
	generated.P(generated.QualifiedGoIdent(sortPackage.Ident("Strings")), "(", keysName, ")")
	generated.P("for _, ", keyName, " := range ", keysName, " {")
	generated.P(valueName, " := message.", field.GoName, "[", keyName, "]")
	if validated.mapKeyProgram != nil {
		emitPredicate(generated, message, field, validated.mapKeyProgram, keyName)
	}
	if validated.mapValueProgram != nil {
		emitPredicate(generated, message, field, validated.mapValueProgram, valueName)
	}
	generated.P("}")
}

func emitPredicate(
	generated *protogen.GeneratedFile,
	message *protogen.Message,
	field *protogen.Field,
	program *expr.Program,
	value string,
) {
	generated.P("if !(", program.Expr, ") {")
	generated.P("return &", generated.QualifiedGoIdent(runtimePackage.Ident("Error")), "{")
	generated.P("Message: ", strconv.Quote(string(message.Desc.FullName())), ",")
	generated.P("Field: ", strconv.Quote(string(field.Desc.Name())), ",")
	generated.P("Predicate: ", strconv.Quote(program.Source), ",")
	generated.P("Value: ", value, ",")
	generated.P("}")
	generated.P("}")
}
