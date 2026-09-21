// Package storedb is the sqlc-generated query layer over the CandaceOS control
// schema.
//
// It is generated from services/candaceos/store/migrations and
// services/candaceos/store/queries.sql by services/candaceos/store/generate.sh
// and must never be edited by hand. It is internal on purpose: every type here
// mirrors the relational schema, so it changes whenever a migration does and
// carries no compatibility promise. Reach durable control-plane state through
// services/candaceos/store instead.
package storedb
