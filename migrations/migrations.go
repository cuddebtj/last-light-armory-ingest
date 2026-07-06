// Package migrations embeds the SQL migration files so the ingest binary can
// migrate the database itself at startup (no external migrate CLI needed).
//
// Files follow golang-migrate naming: {version}_{title}.{up|down}.sql.
package migrations

import "embed"

// FS holds every versioned migration file in this directory.
//
//go:embed *.sql
var FS embed.FS
