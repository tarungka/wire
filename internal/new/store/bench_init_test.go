package store

import (
	"os"
	"testing"

	"github.com/rs/zerolog"
)

// TestMain silences the global zerolog logger so benchmark numbers reflect
// the hot path without trace/debug log overhead.
func TestMain(m *testing.M) {
	zerolog.SetGlobalLevel(zerolog.Disabled)
	os.Exit(m.Run())
}
