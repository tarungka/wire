package engine

import (
	"context"
	"fmt"
	"runtime/debug"

	"github.com/rs/zerolog"
)

// OperatorPanicError preserves the panic stack across task and RPC boundaries.
type OperatorPanicError struct {
	Value any
	Stack string
}

func (e *OperatorPanicError) Error() string { return fmt.Sprintf("%v: %v", ErrOperatorPanic, e.Value) }
func (e *OperatorPanicError) Unwrap() error { return ErrOperatorPanic }

// invokeOperator contains panics at lifecycle and source goroutine boundaries.
// Processing errors use the chain's error policy instead.
func invokeOperator(fn func() error) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = &OperatorPanicError{Value: r, Stack: string(debug.Stack())}
		}
	}()
	return fn()
}

// openOperators returns an idempotent cleanup function. It also unwinds a
// partially initialized chain on errors or panics, in reverse open order.
func openOperators(ctx context.Context, operators []Operator, log zerolog.Logger) (func(), error) {
	opened := 0
	closeOperators := func() {
		for opened > 0 {
			opened--
			if err := invokeOperator(operators[opened].Close); err != nil {
				log.Warn().Err(err).Int("operator", opened).Msg("operator close error")
			}
		}
	}
	for i, op := range operators {
		err := invokeOperator(func() error { return op.Open(ctx) })
		if err != nil {
			closeOperators()
			return nil, fmt.Errorf("operator[%d] open: %w", i, err)
		}
		opened++
	}
	return closeOperators, nil
}
