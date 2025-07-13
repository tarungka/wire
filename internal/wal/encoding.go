package wal

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"time"
)

// encodeEntry encodes an entry to binary format.
func encodeEntry(entry *Entry) ([]byte, error) {
	buf := new(bytes.Buffer)

	// Write offset
	if err := binary.Write(buf, binary.BigEndian, entry.Offset); err != nil {
		return nil, fmt.Errorf("failed to write offset: %w", err)
	}

	// Write timestamp
	if err := binary.Write(buf, binary.BigEndian, entry.Timestamp.UnixNano()); err != nil {
		return nil, fmt.Errorf("failed to write timestamp: %w", err)
	}

	// Write source ID
	if err := writeString(buf, entry.SourceID); err != nil {
		return nil, fmt.Errorf("failed to write source ID: %w", err)
	}

	// Write partition ID
	if err := binary.Write(buf, binary.BigEndian, entry.PartitionID); err != nil {
		return nil, fmt.Errorf("failed to write partition ID: %w", err)
	}

	// Write headers count
	if err := binary.Write(buf, binary.BigEndian, int32(len(entry.Headers))); err != nil {
		return nil, fmt.Errorf("failed to write headers count: %w", err)
	}

	// Write headers
	for k, v := range entry.Headers {
		if err := writeString(buf, k); err != nil {
			return nil, fmt.Errorf("failed to write header key: %w", err)
		}
		if err := writeString(buf, v); err != nil {
			return nil, fmt.Errorf("failed to write header value: %w", err)
		}
	}

	// Write key
	if err := writeBytes(buf, entry.Key); err != nil {
		return nil, fmt.Errorf("failed to write key: %w", err)
	}

	// Write value
	if err := writeBytes(buf, entry.Value); err != nil {
		return nil, fmt.Errorf("failed to write value: %w", err)
	}

	// Write CRC32
	if err := binary.Write(buf, binary.BigEndian, entry.CRC32); err != nil {
		return nil, fmt.Errorf("failed to write CRC32: %w", err)
	}

	return buf.Bytes(), nil
}

// decodeEntry decodes an entry from binary format.
func decodeEntry(data []byte) (*Entry, error) {
	buf := bytes.NewReader(data)
	entry := &Entry{}

	// Read offset
	if err := binary.Read(buf, binary.BigEndian, &entry.Offset); err != nil {
		return nil, fmt.Errorf("failed to read offset: %w", err)
	}

	// Read timestamp
	var timestampNano int64
	if err := binary.Read(buf, binary.BigEndian, &timestampNano); err != nil {
		return nil, fmt.Errorf("failed to read timestamp: %w", err)
	}
	entry.Timestamp = time.Unix(0, timestampNano)

	// Read source ID
	var err error
	entry.SourceID, err = readString(buf)
	if err != nil {
		return nil, fmt.Errorf("failed to read source ID: %w", err)
	}

	// Read partition ID
	if err := binary.Read(buf, binary.BigEndian, &entry.PartitionID); err != nil {
		return nil, fmt.Errorf("failed to read partition ID: %w", err)
	}

	// Read headers count
	var headersCount int32
	if err := binary.Read(buf, binary.BigEndian, &headersCount); err != nil {
		return nil, fmt.Errorf("failed to read headers count: %w", err)
	}

	// Read headers
	entry.Headers = make(map[string]string, headersCount)
	for i := int32(0); i < headersCount; i++ {
		key, err := readString(buf)
		if err != nil {
			return nil, fmt.Errorf("failed to read header key: %w", err)
		}
		value, err := readString(buf)
		if err != nil {
			return nil, fmt.Errorf("failed to read header value: %w", err)
		}
		entry.Headers[key] = value
	}

	// Read key
	entry.Key, err = readBytes(buf)
	if err != nil {
		return nil, fmt.Errorf("failed to read key: %w", err)
	}

	// Read value
	entry.Value, err = readBytes(buf)
	if err != nil {
		return nil, fmt.Errorf("failed to read value: %w", err)
	}

	// Read CRC32
	if err := binary.Read(buf, binary.BigEndian, &entry.CRC32); err != nil {
		return nil, fmt.Errorf("failed to read CRC32: %w", err)
	}

	return entry, nil
}

// writeString writes a string with length prefix.
func writeString(w io.Writer, s string) error {
	if err := binary.Write(w, binary.BigEndian, int32(len(s))); err != nil {
		return err
	}
	_, err := w.Write([]byte(s))
	return err
}

// readString reads a string with length prefix.
func readString(r io.Reader) (string, error) {
	var length int32
	if err := binary.Read(r, binary.BigEndian, &length); err != nil {
		return "", err
	}

	if length < 0 || length > 1024*1024 { // Max 1MB for strings
		return "", fmt.Errorf("invalid string length: %d", length)
	}

	if length == 0 {
		return "", nil
	}

	data := make([]byte, length)
	if _, err := io.ReadFull(r, data); err != nil {
		return "", err
	}

	return string(data), nil
}

// writeBytes writes bytes with length prefix.
func writeBytes(w io.Writer, b []byte) error {
	if err := binary.Write(w, binary.BigEndian, int32(len(b))); err != nil {
		return err
	}
	if len(b) > 0 {
		_, err := w.Write(b)
		return err
	}
	return nil
}

// readBytes reads bytes with length prefix.
func readBytes(r io.Reader) ([]byte, error) {
	var length int32
	if err := binary.Read(r, binary.BigEndian, &length); err != nil {
		return nil, err
	}

	if length < 0 || length > 10*1024*1024 { // Max 10MB for byte arrays
		return nil, fmt.Errorf("invalid bytes length: %d", length)
	}

	if length == 0 {
		return nil, nil
	}

	data := make([]byte, length)
	if _, err := io.ReadFull(r, data); err != nil {
		return nil, err
	}

	return data, nil
}

// timeFromUnixNano converts Unix nanoseconds to time.Time.
func timeFromUnixNano(nano int64) time.Time {
	return time.Unix(0, nano)
}
