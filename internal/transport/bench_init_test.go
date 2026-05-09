package transport

import (
	"os"
	"testing"

	"github.com/rs/zerolog"
)

// TestMain silences the global zerolog logger during benchmarks so timing
// output isn't flooded with "listening" / "session opened" lines from
// loopback Mux setup.
func TestMain(m *testing.M) {
	zerolog.SetGlobalLevel(zerolog.Disabled)
	os.Exit(m.Run())
}
