package dbmigrations

import "embed"

// FS holds the goose SQL migrations so the binary is self-contained.
//
//go:embed migrations/*.sql
var FS embed.FS

const Dir = "migrations"
