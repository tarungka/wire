package secretconfig

import (
	"encoding/json"
	"io"

	"github.com/rs/zerolog"
)

// Logger filters an existing logger's structured events while retaining its
// destination and level. Application-provided destinations and hooks remain
// trusted code: they must not independently emit raw configuration or errors.
func (r *Redactor) Logger(log zerolog.Logger) zerolog.Logger {
	if r == nil {
		return log
	}
	// The incoming event already contains its context and was sampled. Reusing
	// unfiltered context on the destination would reintroduce credentials.
	destination := log.With().Reset().Logger().Sample(nil)
	return log.Output(r.LogWriter(loggerDestination{log: destination}))
}

type loggerDestination struct{ log zerolog.Logger }

func (w loggerDestination) Write(p []byte) (int, error) { return w.WriteLevel(zerolog.NoLevel, p) }
func (w loggerDestination) WriteLevel(level zerolog.Level, p []byte) (int, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(p, &fields); err != nil {
		return 0, io.ErrUnexpectedEOF
	}
	event := w.log.WithLevel(level)
	for key, value := range fields {
		if key == zerolog.LevelFieldName {
			continue
		}
		event = event.RawJSON(key, value)
	}
	event.Send()
	return len(p), nil
}
