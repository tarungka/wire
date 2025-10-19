package pool

import "errors"

var (
	errFactoryNotDefined = errors.New("factory cannot be nil")
	errInvalidPoolSize   = errors.New("pool size needs to be greater than zero")
	errConnNotDefined    = errors.New("connection cannot be nil")

	ErrClosed = errors.New("pool is closed")
)
