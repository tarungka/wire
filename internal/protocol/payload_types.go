package protocol

type fieldKind uint8

const (
	unknownField fieldKind = iota
	integerField
	stringField
	binaryField
	nullableBinaryField
	headersField
	floatField
	boolField
)

func messageFieldKind(message any, key string) fieldKind {
	switch message.(type) {
	case *DataRecordMsg:
		switch key {
		case "k":
			return nullableBinaryField
		case "v":
			return binaryField
		case "t":
			return integerField
		case "h":
			return headersField
		}
	case *StreamHeaderMsg:
		switch key {
		case "src", "dst":
			return stringField
		case "p":
			return integerField
		}
	case *SessionHandshakeMsg:
		switch key {
		case "v", "min_v", "f", "lp":
			return integerField
		case "n":
			return stringField
		}
	case *CheckpointBarrierMsg:
		switch key {
		case "c", "e", "ts":
			return integerField
		}
	case *WatermarkMsg:
		switch key {
		case "t":
			return integerField
		case "s":
			return stringField
		}
	case *EndOfPartitionMsg:
		switch key {
		case "s":
			return stringField
		case "r":
			return integerField
		}
	case *BackpressureMsg:
		switch key {
		case "id", "st":
			return integerField
		case "bu":
			return floatField
		}
	case *SessionDrainMsg:
		if key == "r" {
			return boolField
		}
	}
	return unknownField
}

func (c *payloadCursor) field(kind fieldKind) bool {
	if c.pos == len(c.data) {
		return false
	}
	tag := c.data[c.pos]
	switch kind {
	case integerField:
		integer := tag <= 0x7f || tag >= 0xe0 || (tag >= 0xcc && tag <= 0xd3)
		if !integer {
			return false
		}
	case stringField:
		str := (tag >= 0xa0 && tag <= 0xbf) || tag == 0xd9 || tag == 0xda || tag == 0xdb
		if !str {
			return false
		}
	case binaryField, nullableBinaryField:
		binary := tag >= 0xc4 && tag <= 0xc6
		null := kind == nullableBinaryField && tag == 0xc0
		if !binary && !null {
			return false
		}
	case floatField:
		if tag != 0xca && tag != 0xcb {
			return false
		}
	case boolField:
		if tag != 0xc2 && tag != 0xc3 {
			return false
		}
	case headersField:
		count, ok := c.mapSize()
		if !ok || count > uint64(len(c.data)-c.pos)/2 {
			return false
		}
		for ; count > 0; count-- {
			if _, ok := c.key(); !ok {
				return false
			}
			if !c.field(binaryField) {
				return false
			}
		}
		return true
	}
	return c.skip(0)
}

// canonicalMessage preserves caller ownership while encoding empty byte values
// as binary, never nil. Optional nil keys and empty header maps remain omitted.
func canonicalMessage(message any) any {
	switch m := message.(type) {
	case *DataRecordMsg:
		if m == nil || !needsCanonicalRecord(m) {
			return message
		}
		return canonicalRecord(*m)
	case DataRecordMsg:
		if !needsCanonicalRecord(&m) {
			return message
		}
		return canonicalRecord(m)
	default:
		return message
	}
}

func needsCanonicalRecord(record *DataRecordMsg) bool {
	if record.Value == nil {
		return true
	}
	for _, value := range record.Headers {
		if value == nil {
			return true
		}
	}
	return false
}

func canonicalRecord(record DataRecordMsg) *DataRecordMsg {
	if record.Value == nil {
		record.Value = []byte{}
	}
	for _, value := range record.Headers {
		if value == nil {
			headers := make(map[string][]byte, len(record.Headers))
			for key, value := range record.Headers {
				if value == nil {
					value = []byte{}
				}
				headers[key] = value
			}
			record.Headers = headers
			break
		}
	}
	return &record
}
