package store

import _ "embed"

// schemaSQL is the v1 schema, applied once (PRAGMA user_version gate).
//
//go:embed schema.sql
var schemaSQL string
