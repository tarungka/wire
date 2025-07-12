package wal

import (
	"fmt"
	"strings"

	"github.com/klauspost/compress/snappy"
	"github.com/klauspost/compress/zstd"
)

// CompressionType defines the type of compression to use for WAL entries.
type CompressionType int

const (
	// CompressionNone indicates no compression.
	CompressionNone CompressionType = iota
	// CompressionSnappy indicates Snappy compression.
	CompressionSnappy
	// CompressionZSTD indicates Zstandard compression.
	CompressionZSTD
)

// ParseCompressionType converts a string representation of compression type
// into the CompressionType enum.
func ParseCompressionType(t string) (CompressionType, error) {
	switch strings.ToLower(t) {
	case "none", "": // Default to none
		return CompressionNone, nil
	case "snappy":
		return CompressionSnappy, nil
	case "zstd":
		return CompressionZSTD, nil
	default:
		return CompressionNone, fmt.Errorf("unsupported compression type: %s", t)
	}
}

// compress applies compression to the given data based on the WAL's configuration.
func (w *WAL) compress(data []byte) ([]byte, error) {
	switch w.compressionType {
	case CompressionSnappy:
		return snappy.Encode(nil, data), nil
	case CompressionZSTD:
		encoder, err := zstd.NewWriter(nil)
		if err != nil {
			return nil, err
		}
		return encoder.EncodeAll(data, nil), nil
	case CompressionNone:
		return data, nil
	default:
		return data, nil
	}
}

// decompress applies decompression to the given data based on the WAL's configuration.
func (w *WAL) decompress(data []byte) ([]byte, error) {
	switch w.compressionType {
	case CompressionSnappy:
		return snappy.Decode(nil, data)
	case CompressionZSTD:
		decoder, err := zstd.NewReader(nil)
		if err != nil {
			return nil, err
		}
		return decoder.DecodeAll(data, nil)
	case CompressionNone:
		return data, nil
	default:
		return data, nil
	}
}
