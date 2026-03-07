package keygroup

import "github.com/twmb/murmur3"

// KeyGroup returns the key group for the given key. Hot path — zero allocs.
// The numKeyGroups parameter must be validated before calling (see Config.Validate).
func KeyGroup(key []byte, numKeyGroups int) uint16 {
	return uint16(murmur3.Sum32(key) % uint32(numKeyGroups))
}
