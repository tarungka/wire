package keygroup

import "encoding/binary"

// Pebble composite key layout constants per WIP-03 Section 2.3.
const (
	KeyGroupPrefixSize = 2
	OperatorIDSize     = 4
	MinPebbleKeySize   = KeyGroupPrefixSize + OperatorIDSize // 6
)

// EncodePebbleKey encodes a composite Pebble key:
//
//	[KeyGroupPrefix (2B BE)][OperatorID (4B BE)][UserKey (N bytes)][Namespace (M bytes)]
//
// Big-endian encoding ensures lexicographic byte ordering matches numeric ordering.
func EncodePebbleKey(keyGroup uint16, operatorID uint32, userKey, namespace []byte) []byte {
	size := KeyGroupPrefixSize + OperatorIDSize + len(userKey) + len(namespace)
	buf := make([]byte, size)
	binary.BigEndian.PutUint16(buf[0:2], keyGroup)
	binary.BigEndian.PutUint32(buf[2:6], operatorID)
	copy(buf[6:6+len(userKey)], userKey)
	copy(buf[6+len(userKey):], namespace)
	return buf
}

// DecodePebbleKey decodes the key group and operator ID from a composite Pebble key.
// The remainder (userKey + namespace) is returned as a single slice because the
// boundary between them is operator-specific.
//
// Note: the returned remainder aliases the input slice's backing array.
// Callers that need the data to outlive the input buffer (e.g. across Pebble
// iterator reuse) must copy the remainder.
func DecodePebbleKey(key []byte) (keyGroup uint16, operatorID uint32, remainder []byte, err error) {
	if len(key) < MinPebbleKeySize {
		return 0, 0, nil, ErrPebbleKeyTooShort
	}
	keyGroup = binary.BigEndian.Uint16(key[0:2])
	operatorID = binary.BigEndian.Uint32(key[2:6])
	remainder = key[6:]
	return keyGroup, operatorID, remainder, nil
}

// PebbleRangeStart returns a 2-byte big-endian prefix for the given key group,
// suitable as a seek target for Pebble range scans.
func PebbleRangeStart(keyGroup uint16) []byte {
	buf := make([]byte, KeyGroupPrefixSize)
	binary.BigEndian.PutUint16(buf, keyGroup)
	return buf
}
