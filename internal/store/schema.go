package store

import _ "embed"

// schemaSQL is the v1 schema, applied once (PRAGMA user_version gate). Later
// versions are incremental ALTERs in Store.migrate.
//
//go:embed schema.sql
var schemaSQL string
