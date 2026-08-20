// Package migrations embeds the ordered SQL migration set into the backend binary.
package migrations

import "embed"

// FS contains every numbered SQL migration shipped with this backend build.
// Embedding the files makes startup migration behavior independent of the runtime
// working directory or container volume layout.
//
//go:embed *.sql
var FS embed.FS
