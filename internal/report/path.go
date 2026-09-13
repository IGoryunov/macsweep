package report

import (
	"path/filepath"
	"strings"
)

// DisplayPath replaces the home prefix with "~". It is deliberately a plain
// string operation: the bash prototype's ${var/#pat/~} expanded the tilde back
// into the home path, which is exactly what must not happen here.
func DisplayPath(p, home string) string {
	home = filepath.Clean(home)
	if p == home {
		return "~"
	}
	if strings.HasPrefix(p, home+string(filepath.Separator)) {
		return "~" + p[len(home):]
	}
	return p
}
