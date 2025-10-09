package tcp

// NameAddress wraps a string and implements the
// net.Listener interface.
type NameAddress struct {
	Address string
}

// Network returns the network type. Always returns "tcp".
func (n NameAddress) Network() string {
	// TODO: Implementation truncated
	return ""
}

// String returns the address.
func (n NameAddress) String() string {
	// TODO: Implementation truncated
	return ""
}
