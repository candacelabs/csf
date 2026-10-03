// Package storedb is the sqlc-generated query layer over the deploy control
// schema.
//
// It is generated from services/deploy/store/migrations and
// services/deploy/store/queries.sql by services/deploy/store/generate.sh
// and must never be edited by hand. It is internal on purpose: every type here
// mirrors the relational schema, so it changes whenever a migration does and
// carries no compatibility promise. Reach durable control-plane state through
// services/deploy/store instead.
package storedb
