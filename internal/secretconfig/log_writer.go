package secretconfig

import (
	"bytes"
	"encoding/json"
	"io"

	"github.com/rs/zerolog"
)

// LogWriter filters structured events before they reach any log destination.
// Construct the task logger on this writer before supplying it to factories and
// the runtime. The destination must support the logger's concurrent use.
func (r *Redactor) LogWriter(destination io.Writer) zerolog.LevelWriter {
	return &redactingLogWriter{redactor: r, destination: destination}
}

type redactingLogWriter struct {
	redactor    *Redactor
	destination io.Writer
}

func (w *redactingLogWriter) Write(p []byte) (int, error) { return w.WriteLevel(zerolog.NoLevel, p) }
func (w *redactingLogWriter) WriteLevel(level zerolog.Level, p []byte) (int, error) {
	// Decode before filtering: replacing raw JSON escape sequences can break the
	// syntax or expose a partially escaped credential. Preserve numbers exactly.
	dec := json.NewDecoder(bytes.NewReader(p))
	dec.UseNumber()
	var value any
	err := dec.Decode(&value)
	if err == nil {
		var extra any
		if dec.Decode(&extra) != io.EOF {
			err = io.ErrUnexpectedEOF
		}
	}
	var result []byte
	if err == nil {
		result, err = json.Marshal(w.redactor.logValue(value))
	}
	if err != nil {
		result = []byte(`{"message":"[REDACTED: invalid structured log event]"}`)
	}
	result = append(result, '\n')
	var n int
	if destination, ok := w.destination.(zerolog.LevelWriter); ok {
		n, err = destination.WriteLevel(level, result)
	} else {
		n, err = w.destination.Write(result)
	}
	if err == nil && n != len(result) {
		err = io.ErrShortWrite
	}
	if err != nil {
		return 0, err
	}
	return len(p), nil
}

func (r *Redactor) logValue(value any) any {
	switch value := value.(type) {
	case string:
		return r.String(value)
	case json.Number:
		if filtered := r.String(value.String()); filtered != value.String() {
			return filtered
		}
		return value
	case []any:
		for i := range value {
			value[i] = r.logValue(value[i])
		}
		return value
	case map[string]any:
		result := make(map[string]any, len(value))
		for key, child := range value {
			result[r.String(key)] = r.logValue(child)
		}
		return result
	default:
		return value
	}
}
