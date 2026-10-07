// Copyright 2026 Candace Labs

package scip

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"os"

	"google.golang.org/protobuf/encoding/protowire"
)

// The SCIP wire schema, pinned to the field numbers the SCIP protobuf schema
// declares (scip.proto, package scip). They are listed once here and used by
// the decoders below; TestDecodeAgainstRealIndex decodes an index a real
// indexer wrote, so a changed number fails a test rather than an index.
const (
	indexFieldMetadata        protowire.Number = 1
	indexFieldDocuments       protowire.Number = 2
	indexFieldExternalSymbols protowire.Number = 3

	documentFieldRelativePath protowire.Number = 1
	documentFieldOccurrences  protowire.Number = 2
	documentFieldSymbols      protowire.Number = 3
	documentFieldLanguage     protowire.Number = 4

	occurrenceFieldRange           protowire.Number = 1 // deprecated packed range
	occurrenceFieldSymbol          protowire.Number = 2
	occurrenceFieldSymbolRoles     protowire.Number = 3
	occurrenceFieldSingleLineRange protowire.Number = 8
	occurrenceFieldMultiLineRange  protowire.Number = 9

	singleLineRangeFieldLine           protowire.Number = 1
	singleLineRangeFieldStartCharacter protowire.Number = 2
	singleLineRangeFieldEndCharacter   protowire.Number = 3

	multiLineRangeFieldStartLine      protowire.Number = 1
	multiLineRangeFieldStartCharacter protowire.Number = 2
	multiLineRangeFieldEndLine        protowire.Number = 3
	multiLineRangeFieldEndCharacter   protowire.Number = 4

	metadataFieldToolInfo    protowire.Number = 2
	metadataFieldProjectRoot protowire.Number = 3

	toolInfoFieldName      protowire.Number = 1
	toolInfoFieldVersion   protowire.Number = 2
	toolInfoFieldArguments protowire.Number = 3

	symbolInformationFieldSymbol        protowire.Number = 1
	symbolInformationFieldDocumentation protowire.Number = 3
	symbolInformationFieldRelationships protowire.Number = 4
	symbolInformationFieldKind          protowire.Number = 5

	relationshipFieldSymbol           protowire.Number = 1
	relationshipFieldIsReference      protowire.Number = 2
	relationshipFieldIsImplementation protowire.Number = 3
	relationshipFieldIsTypeDefinition protowire.Number = 4
	relationshipFieldIsDefinition     protowire.Number = 5
)

// Index is a decoded SCIP index.
type Index struct {
	Metadata  Metadata
	Documents []Document
}

// Metadata is the index's metadata: which tool wrote it and where its root is.
type Metadata struct {
	ToolName    string
	ToolVersion string
	ProjectRoot string
}

// Document is one indexed source file.
type Document struct {
	RelativePath string
	Language     string
	Occurrences  []Occurrence
	Symbols      []SymbolInformation
}

// Occurrence is one appearance of a symbol in a document.
type Occurrence struct {
	Symbol      string
	SymbolRoles int32
	Range       Range
}

// Range is a half-open [start, end) source range, 0-based.
type Range struct {
	StartLine      int32
	StartCharacter int32
	EndLine        int32
	EndCharacter   int32
}

// SymbolInformation is what the index knows about a symbol, a document defines.
type SymbolInformation struct {
	Symbol        string
	Kind          int32
	Documentation []string
	Relationships []Relationship
}

// Relationship is a named edge from a symbol to another symbol.
type Relationship struct {
	Symbol           string
	IsReference      bool
	IsImplementation bool
	IsTypeDefinition bool
	IsDefinition     bool
}

// SymbolRole is the bitset SCIP sets on an occurrence. The values are the bits
// the SCIP schema assigns; a role is present when its bit is set.
const (
	RoleDefinition        int32 = 0x1
	RoleImport            int32 = 0x2
	RoleWriteAccess       int32 = 0x4
	RoleReadAccess        int32 = 0x8
	RoleGenerated         int32 = 0x10
	RoleTest              int32 = 0x20
	RoleForwardDefinition int32 = 0x40
)

// HasRole reports whether the occurrence carries the role bit.
func (o Occurrence) HasRole(role int32) bool { return o.SymbolRoles&role != 0 }

// ReadFile decodes the SCIP index a .scip file holds. A SCIP file is the Index
// message gzipped; a little endian gzip stream is the only framing.
func ReadFile(path string) (Index, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Index{}, err
	}
	return Unmarshal(data)
}

// Unmarshal decodes SCIP index bytes, gzipped or not. An indexer always gzips,
// but a fixture and an in-memory index are sometimes raw, so both are accepted.
func Unmarshal(data []byte) (Index, error) {
	raw := data
	if len(data) >= 2 && data[0] == 0x1f && data[1] == 0x8b {
		reader, err := gzip.NewReader(bytes.NewReader(data))
		if err != nil {
			return Index{}, fmt.Errorf("scip: gunzip index: %w", err)
		}
		defer reader.Close()
		raw, err = io.ReadAll(bufio.NewReader(reader))
		if err != nil {
			return Index{}, fmt.Errorf("scip: read gunzipped index: %w", err)
		}
	}
	var index Index
	if err := index.unmarshal(raw); err != nil {
		return Index{}, err
	}
	return index, nil
}

func (index *Index) unmarshal(b []byte) error {
	return eachField(b, func(num protowire.Number, typ protowire.Type, value []byte) error {
		switch num {
		case indexFieldMetadata:
			payload, err := bytesValue(typ, value)
			if err != nil {
				return err
			}
			return index.Metadata.unmarshal(payload)
		case indexFieldDocuments:
			payload, err := bytesValue(typ, value)
			if err != nil {
				return err
			}
			var document Document
			if err := document.unmarshal(payload); err != nil {
				return err
			}
			index.Documents = append(index.Documents, document)
		}
		return nil
	})
}

func (metadata *Metadata) unmarshal(b []byte) error {
	return eachField(b, func(num protowire.Number, typ protowire.Type, value []byte) error {
		switch num {
		case metadataFieldProjectRoot:
			text, err := stringValue(typ, value)
			if err != nil {
				return err
			}
			metadata.ProjectRoot = text
		case metadataFieldToolInfo:
			payload, err := bytesValue(typ, value)
			if err != nil {
				return err
			}
			return metadata.unmarshalToolInfo(payload)
		}
		return nil
	})
}

func (metadata *Metadata) unmarshalToolInfo(b []byte) error {
	return eachField(b, func(num protowire.Number, typ protowire.Type, value []byte) error {
		text, err := stringValue(typ, value)
		if err != nil {
			return err
		}
		switch num {
		case toolInfoFieldName:
			metadata.ToolName = text
		case toolInfoFieldVersion:
			metadata.ToolVersion = text
		}
		return nil
	})
}

func (document *Document) unmarshal(b []byte) error {
	return eachField(b, func(num protowire.Number, typ protowire.Type, value []byte) error {
		switch num {
		case documentFieldRelativePath, documentFieldLanguage:
			text, err := stringValue(typ, value)
			if err != nil {
				return err
			}
			if num == documentFieldRelativePath {
				document.RelativePath = text
			} else {
				document.Language = text
			}
		case documentFieldOccurrences:
			payload, err := bytesValue(typ, value)
			if err != nil {
				return err
			}
			var occurrence Occurrence
			if err := occurrence.unmarshal(payload); err != nil {
				return err
			}
			document.Occurrences = append(document.Occurrences, occurrence)
		case documentFieldSymbols:
			payload, err := bytesValue(typ, value)
			if err != nil {
				return err
			}
			var symbol SymbolInformation
			if err := symbol.unmarshal(payload); err != nil {
				return err
			}
			document.Symbols = append(document.Symbols, symbol)
		}
		return nil
	})
}

func (occurrence *Occurrence) unmarshal(b []byte) error {
	return eachField(b, func(num protowire.Number, typ protowire.Type, value []byte) error {
		switch num {
		case occurrenceFieldSymbol:
			text, err := stringValue(typ, value)
			if err != nil {
				return err
			}
			occurrence.Symbol = text
		case occurrenceFieldSymbolRoles:
			roles, err := varintValue(typ, value)
			if err != nil {
				return err
			}
			occurrence.SymbolRoles = int32(roles)
		case occurrenceFieldRange:
			rng, err := decodeDeprecatedRange(typ, value)
			if err != nil {
				return err
			}
			occurrence.Range = rng
		case occurrenceFieldSingleLineRange:
			payload, err := bytesValue(typ, value)
			if err != nil {
				return err
			}
			rng, err := decodeSingleLineRange(payload)
			if err != nil {
				return err
			}
			occurrence.Range = rng
		case occurrenceFieldMultiLineRange:
			payload, err := bytesValue(typ, value)
			if err != nil {
				return err
			}
			rng, err := decodeMultiLineRange(payload)
			if err != nil {
				return err
			}
			occurrence.Range = rng
		}
		return nil
	})
}

// decodeDeprecatedRange reads the deprecated `repeated int32 range` field, which
// is packed by default but may arrive unpacked.
func decodeDeprecatedRange(typ protowire.Type, value []byte) (Range, error) {
	var values []int32
	if typ == protowire.BytesType {
		packed, err := bytesValue(typ, value)
		if err != nil {
			return Range{}, err
		}
		for len(packed) > 0 {
			element, n := protowire.ConsumeVarint(packed)
			if n < 0 {
				return Range{}, protowire.ParseError(n)
			}
			values = append(values, int32(element))
			packed = packed[n:]
		}
	} else {
		element, err := varintValue(typ, value)
		if err != nil {
			return Range{}, err
		}
		values = append(values, int32(element))
	}
	switch len(values) {
	case 3:
		return Range{StartLine: values[0], StartCharacter: values[1], EndLine: values[0], EndCharacter: values[2]}, nil
	case 4:
		return Range{StartLine: values[0], StartCharacter: values[1], EndLine: values[2], EndCharacter: values[3]}, nil
	default:
		return Range{}, nil
	}
}

func decodeSingleLineRange(b []byte) (Range, error) {
	var line, start, end int32
	if err := eachField(b, func(num protowire.Number, typ protowire.Type, value []byte) error {
		number, err := varintValue(typ, value)
		if err != nil {
			return err
		}
		switch num {
		case singleLineRangeFieldLine:
			line = int32(number)
		case singleLineRangeFieldStartCharacter:
			start = int32(number)
		case singleLineRangeFieldEndCharacter:
			end = int32(number)
		}
		return nil
	}); err != nil {
		return Range{}, err
	}
	return Range{StartLine: line, StartCharacter: start, EndLine: line, EndCharacter: end}, nil
}

func decodeMultiLineRange(b []byte) (Range, error) {
	var rng Range
	if err := eachField(b, func(num protowire.Number, typ protowire.Type, value []byte) error {
		number, err := varintValue(typ, value)
		if err != nil {
			return err
		}
		switch num {
		case multiLineRangeFieldStartLine:
			rng.StartLine = int32(number)
		case multiLineRangeFieldStartCharacter:
			rng.StartCharacter = int32(number)
		case multiLineRangeFieldEndLine:
			rng.EndLine = int32(number)
		case multiLineRangeFieldEndCharacter:
			rng.EndCharacter = int32(number)
		}
		return nil
	}); err != nil {
		return Range{}, err
	}
	return rng, nil
}

func (symbol *SymbolInformation) unmarshal(b []byte) error {
	return eachField(b, func(num protowire.Number, typ protowire.Type, value []byte) error {
		switch num {
		case symbolInformationFieldSymbol:
			text, err := stringValue(typ, value)
			if err != nil {
				return err
			}
			symbol.Symbol = text
		case symbolInformationFieldDocumentation:
			text, err := stringValue(typ, value)
			if err != nil {
				return err
			}
			symbol.Documentation = append(symbol.Documentation, text)
		case symbolInformationFieldKind:
			kind, err := varintValue(typ, value)
			if err != nil {
				return err
			}
			symbol.Kind = int32(kind)
		case symbolInformationFieldRelationships:
			payload, err := bytesValue(typ, value)
			if err != nil {
				return err
			}
			relationship, err := decodeRelationship(payload)
			if err != nil {
				return err
			}
			symbol.Relationships = append(symbol.Relationships, relationship)
		}
		return nil
	})
}

func decodeRelationship(b []byte) (Relationship, error) {
	var relationship Relationship
	if err := eachField(b, func(num protowire.Number, typ protowire.Type, value []byte) error {
		switch num {
		case relationshipFieldSymbol:
			text, err := stringValue(typ, value)
			if err != nil {
				return err
			}
			relationship.Symbol = text
		case relationshipFieldIsReference,
			relationshipFieldIsImplementation,
			relationshipFieldIsTypeDefinition,
			relationshipFieldIsDefinition:
			flag, err := varintValue(typ, value)
			if err != nil {
				return err
			}
			present := flag != 0
			switch num {
			case relationshipFieldIsReference:
				relationship.IsReference = present
			case relationshipFieldIsImplementation:
				relationship.IsImplementation = present
			case relationshipFieldIsTypeDefinition:
				relationship.IsTypeDefinition = present
			case relationshipFieldIsDefinition:
				relationship.IsDefinition = present
			}
		}
		return nil
	}); err != nil {
		return Relationship{}, err
	}
	return relationship, nil
}

// eachField walks the fields of one protobuf message body, skipping fields the
// caller does not read (a newer indexer's additions) instead of failing.
func eachField(b []byte, visit func(num protowire.Number, typ protowire.Type, value []byte) error) error {
	for len(b) > 0 {
		num, typ, n := protowire.ConsumeTag(b)
		if n < 0 {
			return protowire.ParseError(n)
		}
		b = b[n:]
		valueSize := protowire.ConsumeFieldValue(num, typ, b)
		if valueSize < 0 {
			return protowire.ParseError(valueSize)
		}
		if err := visit(num, typ, b[:valueSize]); err != nil {
			return err
		}
		b = b[valueSize:]
	}
	return nil
}

func bytesValue(typ protowire.Type, value []byte) ([]byte, error) {
	if typ != protowire.BytesType {
		return nil, fmt.Errorf("scip: expected length-delimited field, got %v", typ)
	}
	payload, n := protowire.ConsumeBytes(value)
	if n < 0 {
		return nil, protowire.ParseError(n)
	}
	return payload, nil
}

func stringValue(typ protowire.Type, value []byte) (string, error) {
	payload, err := bytesValue(typ, value)
	if err != nil {
		return "", err
	}
	return string(payload), nil
}

func varintValue(typ protowire.Type, value []byte) (uint64, error) {
	if typ != protowire.VarintType {
		return 0, fmt.Errorf("scip: expected varint field, got %v", typ)
	}
	number, n := protowire.ConsumeVarint(value)
	if n < 0 {
		return 0, protowire.ParseError(n)
	}
	return number, nil
}
