package operators

import (
	"fmt"
	"strings"

	"github.com/tarungka/wire/internal/analytics"
)

func init() {
	Register("uppercase", func(config map[string]interface{}) (analytics.Operator, error) {
		return &UppercaseOperator{}, nil
	})
	Register("logger", func(config map[string]interface{}) (analytics.Operator, error) {
		return &LoggerOperator{}, nil
	})
}

// UppercaseOperator converts string data to uppercase.
type UppercaseOperator struct {
	id string
}

func (o *UppercaseOperator) ID() string { return o.id }
func (o *UppercaseOperator) Open(ctx analytics.OperatorContext) error {
	o.id = ctx.OperatorID()
	return nil
}
func (o *UppercaseOperator) ProcessElement(record *analytics.Record, out analytics.Stream) error {
	if s, ok := record.Data.(string); ok {
		record.Data = strings.ToUpper(s)
	}
	return out.Emit(record)
}
func (o *UppercaseOperator) Close() error { return nil }

// LoggerOperator logs every record to stdout.
type LoggerOperator struct {
	id string
}

func (o *LoggerOperator) ID() string { return o.id }
func (o *LoggerOperator) Open(ctx analytics.OperatorContext) error {
	o.id = ctx.OperatorID()
	return nil
}
func (o *LoggerOperator) ProcessElement(record *analytics.Record, out analytics.Stream) error {
	fmt.Printf("[Operator %s] Record: %v\n", o.id, record.Data)
	return out.Emit(record)
}
func (o *LoggerOperator) Close() error { return nil }
