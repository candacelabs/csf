// Copyright 2026 Candace Labs

// Package scip reads SCIP (Sourcegraph Code Intelligence Protocol) indexes and
// projects them into one fixed fact schema.
//
// SCIP is the language-neutral index format a per-language indexer writes:
// scip-go, scip-typescript, scip-python, scip-java and scip-rust each emit an
// index.scip file describing the documents they indexed, the symbols those
// documents define, and every occurrence of a symbol in a document. The format
// is one protobuf schema for every language, so a reader written once serves
// all of them; only the symbol scheme and the document's language string differ.
//
// This package has three parts, and the mine stage of the bootstrap loop uses
// all three:
//
//   - The wire decode reads the SCIP index: Unmarshal and ReadFile decode the
//     gzipped protobuf an indexer wrote into the Index model in wire.go. It
//     reads the schema with encoding/protowire rather than a generated binding,
//     so the package takes no dependency beyond the protobuf runtime the module
//     already has; the decode test reads a fixture a real indexer wrote, so the
//     field numbers are guarded against a real indexer's output.
//   - The symbol grammar parses a SCIP symbol string (ParseSymbol) into its
//     scheme, package and descriptors, per the grammar the SCIP schema
//     documents.
//   - The fixed fact schema s0 projects an Index into facts (Facts) and checks
//     that a fact stream conforms to the schema (Check). SchemaS0 names every
//     relation and its arity; a fact is one relation applied to arguments with
//     the source span it came from.
package scip
