package protocol

import "encoding/binary"

// payloadCursor walks MessagePack boundaries without allocating a second copy
// of record values. Unknown fields remain forward compatible, with bounded
// nesting and lengths checked against the frame before traversal.
type payloadCursor struct {
	data []byte
	pos  int
}

func (c *payloadCursor) take(n uint64) ([]byte, bool) {
	if n > uint64(len(c.data)-c.pos) {
		return nil, false
	}
	start := c.pos
	c.pos += int(n)
	return c.data[start:c.pos], true
}
func (c *payloadCursor) number(width uint64) (uint64, bool) {
	b, ok := c.take(width)
	if !ok {
		return 0, false
	}
	switch width {
	case 1:
		return uint64(b[0]), true
	case 2:
		return uint64(binary.BigEndian.Uint16(b)), true
	case 4:
		return uint64(binary.BigEndian.Uint32(b)), true
	}
	return 0, false
}
func (c *payloadCursor) mapSize() (uint64, bool) {
	b, ok := c.take(1)
	if !ok {
		return 0, false
	}
	switch {
	case b[0] >= 0x80 && b[0] <= 0x8f:
		return uint64(b[0] & 15), true
	case b[0] == 0xde:
		return c.number(2)
	case b[0] == 0xdf:
		return c.number(4)
	}
	return 0, false
}
func (c *payloadCursor) key() ([]byte, bool) {
	b, ok := c.take(1)
	if !ok {
		return nil, false
	}
	var n uint64
	switch {
	case b[0] >= 0xa0 && b[0] <= 0xbf:
		n = uint64(b[0] & 31)
	case b[0] == 0xd9:
		n, ok = c.number(1)
	case b[0] == 0xda:
		n, ok = c.number(2)
	case b[0] == 0xdb:
		n, ok = c.number(4)
	default:
		return nil, false
	}
	if !ok {
		return nil, false
	}
	return c.take(n)
}
func (c *payloadCursor) skip(depth int) bool {
	if depth > 64 {
		return false
	}
	b, ok := c.take(1)
	if !ok {
		return false
	}
	tag := b[0]
	var size, children uint64
	switch {
	case tag <= 0x7f || tag >= 0xe0 || tag == 0xc0 || tag == 0xc2 || tag == 0xc3:
		return true
	case tag >= 0xa0 && tag <= 0xbf:
		size = uint64(tag & 31)
	case tag >= 0x90 && tag <= 0x9f:
		children = uint64(tag & 15)
	case tag >= 0x80 && tag <= 0x8f:
		children = 2 * uint64(tag&15)
	default:
		switch tag {
		case 0xc4, 0xd9:
			size, ok = c.number(1)
		case 0xc5, 0xda:
			size, ok = c.number(2)
		case 0xc6, 0xdb:
			size, ok = c.number(4)
		case 0xc7:
			size, ok = c.number(1)
			size++
		case 0xc8:
			size, ok = c.number(2)
			size++
		case 0xc9:
			size, ok = c.number(4)
			size++
		case 0xca, 0xce, 0xd2:
			size = 4
		case 0xcb, 0xcf, 0xd3:
			size = 8
		case 0xcc, 0xd0:
			size = 1
		case 0xcd, 0xd1:
			size = 2
		case 0xd4:
			size = 2
		case 0xd5:
			size = 3
		case 0xd6:
			size = 5
		case 0xd7:
			size = 9
		case 0xd8:
			size = 17
		case 0xdc:
			children, ok = c.number(2)
		case 0xdd:
			children, ok = c.number(4)
		case 0xde:
			children, ok = c.number(2)
			children *= 2
		case 0xdf:
			children, ok = c.number(4)
			children *= 2
		default:
			return false
		}
	}
	if !ok {
		return false
	}
	if _, ok = c.take(size); !ok {
		return false
	}
	if children > uint64(len(c.data)-c.pos) {
		return false
	}
	for ; children > 0; children-- {
		if !c.skip(depth + 1) {
			return false
		}
	}
	return true
}

func hasRequiredFields(data []byte, required []string, message any) bool {
	c := payloadCursor{data: data}
	count, ok := c.mapSize()
	if !ok || count > uint64(len(data)-c.pos)/2 {
		return false
	}
	var seen uint16
	for ; count > 0; count-- {
		key, ok := c.key()
		if !ok {
			return false
		}
		for i, name := range required {
			if string(key) == name {
				bit := uint16(1) << i
				if seen&bit != 0 {
					return false
				}
				seen |= bit
				break
			}
		}
		if !c.field(messageFieldKind(message, string(key))) {
			return false
		}
	}
	return seen == (uint16(1)<<len(required))-1 && c.pos == len(data)
}
