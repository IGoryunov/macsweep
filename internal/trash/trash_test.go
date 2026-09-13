package trash

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTrashMovesToTrash(t *testing.T) {
	if err := Available(); err != nil {
		t.Skip(err)
	}
	dir := t.TempDir()
	victim := filepath.Join(dir, "macsweep test 🚀")
	if err := os.MkdirAll(filepath.Join(victim, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(victim, "sub", "f.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	dest, err := Trash(victim)
	if err != nil {
		t.Fatal(err)
	}
	// Tests are allowed to delete: clean up what we put in the Trash.
	defer os.RemoveAll(dest)
	if _, err := os.Lstat(victim); !os.IsNotExist(err) {
		t.Fatalf("original still exists: %v", err)
	}
	if !strings.Contains(dest, ".Trash") {
		t.Errorf("destination %q is not in a Trash directory", dest)
	}
	if _, err := os.Stat(filepath.Join(dest, "sub", "f.txt")); err != nil {
		t.Errorf("content not preserved in Trash: %v", err)
	}
	if _, err := Trash(filepath.Join(dir, "missing")); err == nil {
		t.Error("trashing a missing path must fail")
	}
}
