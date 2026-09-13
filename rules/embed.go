// Package rulesfs holds the compiled-in rule files.
package rulesfs

import (
	"embed"
	"io/fs"
)

//go:embed *.yaml
var files embed.FS

// FS returns the embedded rules directory.
func FS() fs.FS { return files }
