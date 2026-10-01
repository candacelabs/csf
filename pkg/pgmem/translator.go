package pgmem

import (
	"context"
	"fmt"
	"strings"

	pgquery "github.com/pganalyze/pg_query_go/v6"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// ITranslator converts PostgreSQL syntax into the internal execution dialect.
// Implementations must be safe for concurrent use.
type ITranslator interface {
	Translate(ctx context.Context, defaultSchema, statement string) (string, error)
}

type defaultTranslator struct{}

func (defaultTranslator) Translate(ctx context.Context, defaultSchema, statement string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}

	parsed, err := pgquery.Parse(statement)
	if err != nil {
		return "", &Error{
			Code:      "42601",
			Message:   "invalid PostgreSQL syntax",
			Statement: statement,
			Cause:     err,
		}
	}
	for _, rawStatement := range parsed.Stmts {
		if rawStatement == nil || rawStatement.Stmt == nil {
			continue
		}
		rewritePostgreSQLTree(rawStatement.Stmt.ProtoReflect(), defaultSchema)
	}
	if len(parsed.Stmts) == 1 {
		if index := parsed.Stmts[0].Stmt.GetIndexStmt(); index != nil {
			return deparseSQLiteIndex(parsed, index)
		}
	}

	translated, err := pgquery.Deparse(parsed)
	if err != nil {
		return "", fmt.Errorf("deparse PostgreSQL AST: %w", err)
	}
	return strings.TrimSpace(translated), nil
}

func deparseSQLiteIndex(parsed *pgquery.ParseResult, index *pgquery.IndexStmt) (string, error) {
	const (
		btreeAccessMethod       = "btree"
		createIndexPrefix       = "CREATE INDEX "
		createUniqueIndexPrefix = "CREATE UNIQUE INDEX "
		indexIfNotExists        = "IF NOT EXISTS "
	)
	if index.Idxname == "" || index.Relation == nil || index.Concurrent ||
		(index.AccessMethod != "" && index.AccessMethod != btreeAccessMethod) ||
		len(index.IndexIncludingParams) != 0 || len(index.Options) != 0 ||
		index.TableSpace != "" || index.NullsNotDistinct {
		return "", fmt.Errorf("pgmem: only named, ordinary btree indexes are supported: %w", ErrUnsupported)
	}

	// PostgreSQL qualifies the table; SQLite qualifies the index and resolves
	// its unqualified table in that database. Keep the parsed key expressions,
	// predicate, ordering and uniqueness intact in the upstream deparser.
	qualifiedName := quoteIdentifier(index.Relation.Schemaname) + "." + quoteIdentifier(index.Idxname)
	ifNotExists := index.IfNotExists
	index.Relation.Schemaname = ""
	index.Idxname = ""
	index.IfNotExists = false
	index.AccessMethod = ""
	translated, err := pgquery.Deparse(parsed)
	if err != nil {
		return "", fmt.Errorf("deparse PostgreSQL index: %w", err)
	}
	prefix := createIndexPrefix
	if index.Unique {
		prefix = createUniqueIndexPrefix
	}
	definition, found := strings.CutPrefix(translated, prefix)
	if !found {
		return "", fmt.Errorf("pgmem: unexpected PostgreSQL index deparser output: %w", ErrUnsupported)
	}
	if ifNotExists {
		prefix += indexIfNotExists
	}
	return prefix + qualifiedName + " " + definition, nil
}

func rewritePostgreSQLTree(message protoreflect.Message, defaultSchema string) {
	var createdTable *pgquery.CreateStmt
	switch node := message.Interface().(type) {
	case *pgquery.RangeVar:
		switch {
		case node.Schemaname == "public":
			node.Schemaname = "main"
		case node.Schemaname == "" && defaultSchema == "public":
			node.Schemaname = "main"
		case node.Schemaname == "":
			node.Schemaname = defaultSchema
		}
	case *pgquery.TypeName:
		rewritePostgreSQLType(node)
	case *pgquery.A_Expr:
		if node.Kind == pgquery.A_Expr_Kind_AEXPR_ILIKE {
			node.Kind = pgquery.A_Expr_Kind_AEXPR_LIKE
			for _, name := range node.Name {
				if operator := name.GetString_(); operator != nil {
					switch operator.Sval {
					case "~~*":
						operator.Sval = "~~"
					case "!~~*":
						operator.Sval = "!~~"
					}
				}
			}
		}
	case *pgquery.CreateStmt:
		createdTable = node
	case *pgquery.Node:
		// A cast on a parameter (`$2::text`, the form sqlc emits for a typed
		// nullable argument) deparses to text the execution engine's
		// parameter parser reads as the named argument "2::text". The engine
		// is dynamically typed, so the cast carries nothing: keep the bare
		// parameter.
		if cast := node.GetTypeCast(); cast != nil {
			if parameter := cast.GetArg().GetParamRef(); parameter != nil {
				node.Node = &pgquery.Node_ParamRef{ParamRef: parameter}
			}
		}
	}

	message.Range(func(field protoreflect.FieldDescriptor, value protoreflect.Value) bool {
		switch {
		case field.IsList() && field.Kind() == protoreflect.MessageKind:
			list := value.List()
			for index := 0; index < list.Len(); index++ {
				rewritePostgreSQLTree(list.Get(index).Message(), defaultSchema)
			}
		case field.Kind() == protoreflect.MessageKind:
			rewritePostgreSQLTree(value.Message(), defaultSchema)
		}
		return true
	})

	if createdTable != nil && createdTable.Relation != nil {
		for _, element := range createdTable.TableElts {
			clearSameSchemaForeignKeyQualifiers(element.ProtoReflect(), createdTable.Relation.Schemaname)
		}
	}
}

func clearSameSchemaForeignKeyQualifiers(message protoreflect.Message, tableSchema string) {
	// SQLite resolves an unqualified REFERENCES target inside the database
	// containing the table being created and rejects same-database qualifiers.
	// Clear only a qualifier that names that actual database. A target resolved
	// through a different receiver/search path must retain its qualifier so the
	// unsupported cross-schema foreign key fails instead of silently changing
	// meaning.
	if constraint, ok := message.Interface().(*pgquery.Constraint); ok &&
		constraint.Pktable != nil && constraint.Pktable.Schemaname == tableSchema {
		constraint.Pktable.Schemaname = ""
	}

	message.Range(func(field protoreflect.FieldDescriptor, value protoreflect.Value) bool {
		switch {
		case field.IsList() && field.Kind() == protoreflect.MessageKind:
			list := value.List()
			for index := 0; index < list.Len(); index++ {
				clearSameSchemaForeignKeyQualifiers(list.Get(index).Message(), tableSchema)
			}
		case field.Kind() == protoreflect.MessageKind:
			clearSameSchemaForeignKeyQualifiers(value.Message(), tableSchema)
		}
		return true
	})
}

func rewritePostgreSQLType(typeName *pgquery.TypeName) {
	if typeName == nil || len(typeName.Names) == 0 {
		return
	}
	lastName := typeName.Names[len(typeName.Names)-1].GetString_()
	if lastName == nil {
		return
	}

	mapped, exists := map[string]string{
		"bigserial":   "integer",
		"bytea":       "blob",
		"int2":        "integer",
		"int4":        "integer",
		"int8":        "integer",
		"jsonb":       "json",
		"serial":      "integer",
		"serial2":     "integer",
		"serial4":     "integer",
		"serial8":     "integer",
		"smallserial": "integer",
		"timestamptz": "timestamp",
		"uuid":        "text",
	}[strings.ToLower(lastName.Sval)]
	if !exists {
		return
	}
	typeName.Names = []*pgquery.Node{{
		Node: &pgquery.Node_String_{String_: &pgquery.String{Sval: mapped}},
	}}
}
