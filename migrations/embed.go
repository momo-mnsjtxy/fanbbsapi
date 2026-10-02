// Package migrations owns the ordered SQLite and PostgreSQL schemas. Embedding
// the files makes binaries independent of their current working directory.
package migrations

import "embed"

// Files contains every forward-only migration in lexical order.
//
//go:embed *.sql
var Files embed.FS

// PostgresFiles contains the PostgreSQL migration stream. It intentionally
// mirrors the SQLite version names so deployment rehearsals can prove that both
// adapters reached the same logical schema version.
//
//go:embed postgres/*.sql
var PostgresFiles embed.FS
