package migrations

import "embed"

// Files 嵌入全部仅向前的应用迁移。
//
//go:embed *.up.sql
var Files embed.FS
