//go:build manual

package scan

import (
	"context"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestDuConformance compares the scanner with du -sk on a real directory.
// Run with: go test -tags manual -run DuConformance ./internal/scan
func TestDuConformance(t *testing.T) {
	home, _ := os.UserHomeDir()
	dir := os.Getenv("MACSWEEP_DU_DIR")
	if dir == "" {
		dir = filepath.Join(home, "Library", "Caches")
	}
	// du exits 1 when it hits unreadable entries but still prints the total.
	out, err := exec.Command("du", "-sk", dir).Output()
	if err != nil && len(out) == 0 {
		t.Fatal(err)
	}
	kb, _ := strconv.ParseInt(strings.Fields(string(out))[0], 10, 64)
	du := kb * 1024
	res, err := New(Options{}).Measure(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	diff := math.Abs(float64(res.Bytes-du)) / float64(du)
	t.Logf("du=%d scanner=%d diff=%.2f%%", du, res.Bytes, diff*100)
	if diff > 0.02 {
		t.Fatalf("scanner differs from du by %.2f%%", diff*100)
	}
}
