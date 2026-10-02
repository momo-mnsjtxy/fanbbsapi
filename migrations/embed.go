// Package migrations owns the ordered SQLite schema. Embedding the files makes
// local binaries independent of their current working directory.
package migrations

import "embed"

// Files contains every forward-only migration in lexical order.
//
//go:embed *.sql
var Files embed.FS
