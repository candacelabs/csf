// Package liquidproto provides the small runtime used by Liquid Proto
// generated code and deterministic, validating protobuf serialization.
//
// Refinement predicates are compiled into generated Go. This package does not
// interpret predicates at run time. Generated Validate<Message> functions
// enforce scalar and enum refinements at serialization and service boundaries.
package liquidproto
