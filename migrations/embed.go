// Package migrations embeds the SQL migrations into every binary that needs them.
package migrations

import "embed"

//go:embed *.sql
var FS embed.FS
