// Package trash moves items to the Finder Trash through NSFileManager. It is
// the only place in macsweep that removes anything from its original location,
// and it never deletes: "Put Back" in Finder works for everything it moves.
package trash

/*
#cgo CFLAGS: -x objective-c -fobjc-arc
#cgo LDFLAGS: -framework Foundation
#include <stdlib.h>

// Defined in trash_darwin.m. Returns 0 on success and fills *out (caller frees).
// On failure returns non-zero and fills *errmsg (caller frees).
int macsweep_trash(const char *path, char **out, char **errmsg);
*/
import "C"

import (
	"errors"
	"fmt"
	"os"
	"unsafe"
)

// Trasher is what the clean pipeline needs; tests substitute a fake.
type Trasher interface {
	Trash(path string) (trashedPath string, err error)
}

// System is the real NSFileManager-backed Trasher.
type System struct{}

// Trash moves path to the Trash and returns its new location.
func (System) Trash(path string) (string, error) { return Trash(path) }

// Trash moves path to the Trash via trashItemAtURL:resultingItemURL:error:.
func Trash(path string) (string, error) {
	if _, err := os.Lstat(path); err != nil {
		return "", err
	}
	cpath := C.CString(path)
	defer C.free(unsafe.Pointer(cpath))
	var out, errmsg *C.char
	rc := C.macsweep_trash(cpath, &out, &errmsg)
	if rc != 0 {
		msg := "unknown error"
		if errmsg != nil {
			msg = C.GoString(errmsg)
			C.free(unsafe.Pointer(errmsg))
		}
		return "", fmt.Errorf("trash %s: %s", path, msg)
	}
	res := C.GoString(out)
	C.free(unsafe.Pointer(out))
	if res == "" {
		return "", errors.New("trash returned no destination")
	}
	return res, nil
}

// Available reports whether the Trash can be used at all.
func Available() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	if _, err := os.Stat(home + "/.Trash"); err != nil {
		return fmt.Errorf("~/.Trash not accessible: %w", err)
	}
	return nil
}
