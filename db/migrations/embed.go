package migrations

import "embed"

// Files contains the ordered migration SQL embedded in the application binary.
//
//go:embed *.sql
var Files embed.FS
