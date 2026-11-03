// Package migrations embeds the SQL schema migrations (goose format).
package migrations

import "embed"

// FS contains all *.sql migrations.
//
//go:embed *.sql
var FS embed.FS
