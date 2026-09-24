package sdk

import "github.com/tarungka/wire/internal/engine"

// WindowResult identifies an initial or updated window aggregate. Results keep
// their original Value; window identity is carried in reserved event headers.
type WindowResult = engine.WindowResult

// DecodeWindowResult reads window bounds and update identity from a result event.
func DecodeWindowResult(event Event) (WindowResult, bool, error) {
	return engine.DecodeWindowResult(event)
}
