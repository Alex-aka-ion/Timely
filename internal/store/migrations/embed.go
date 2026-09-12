// Package migrations встраивает SQL-миграции в бинарник через embed.FS.
package migrations

import "embed"

// FS содержит все *.sql файлы из этой директории.
//
//go:embed *.sql
var FS embed.FS
