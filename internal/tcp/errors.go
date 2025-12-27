package tcp

import "errors"

var (
	errAddrNil            = errors.New("address cannot be nil")
	errListenerNil        = errors.New("listener cannot be nil")
	errDialerNil          = errors.New("dialer cannot be nil")
	errHeaderAlreadyInUse = errors.New("header already in use")
	errConnClosed         = errors.New("connection closed")
)
