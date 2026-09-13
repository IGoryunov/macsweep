package scan

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/IGoryunov/macsweep/internal/testutil/fixture"
)

func BenchmarkMeasure(b *testing.B) {
	home := fixture.Build(b, fixture.Many("bench", 50000, 100))
	root := filepath.Join(home, "bench")
	s := New(Options{})
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := s.Measure(context.Background(), root); err != nil {
			b.Fatal(err)
		}
	}
}
