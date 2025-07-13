package wal

import (
	"fmt"
	"io"

	"github.com/golang/snappy"
	"github.com/klauspost/compress/zstd"
)

// CompressionType represents the type of compression used for WAL entries.
type CompressionType int

const (
	// CompressionNone represents no compression.
	CompressionNone CompressionType = iota
	// CompressionSnappy represents Snappy compression.
	CompressionSnappy
	// CompressionZstd represents Zstandard compression.
	CompressionZstd
)

// String returns the string representation of the compression type.
func (c CompressionType) String() string {
	switch c {
	case CompressionNone:
		return "none"
	case CompressionSnappy:
		return "snappy"
	case CompressionZstd:
		return "zstd"
	default:
		return "unknown"
	}
}

// ParseCompressionType parses a string into a CompressionType.
func ParseCompressionType(s string) (CompressionType, error) {
	switch s {
	case "none", "":
		return CompressionNone, nil
	case "snappy":
		return CompressionSnappy, nil
	case "zstd":
		return CompressionZstd, nil
	default:
		return CompressionNone, fmt.Errorf("unknown compression type: %s", s)
	}
}

// Compressor defines the interface for compression algorithms.
type Compressor interface {
	// Compress compresses the input data.
	Compress(data []byte) ([]byte, error)
	// Decompress decompresses the input data.
	Decompress(data []byte) ([]byte, error)
}

// NewCompressor creates a new compressor based on the compression type.
func NewCompressor(compressionType CompressionType) Compressor {
	switch compressionType {
	case CompressionSnappy:
		return &snappyCompressor{}
	case CompressionZstd:
		return &zstdCompressor{}
	default:
		return &noopCompressor{}
	}
}

// noopCompressor implements no compression.
type noopCompressor struct{}

func (n *noopCompressor) Compress(data []byte) ([]byte, error) {
	return data, nil
}

func (n *noopCompressor) Decompress(data []byte) ([]byte, error) {
	return data, nil
}

// snappyCompressor implements Snappy compression.
type snappyCompressor struct{}

func (s *snappyCompressor) Compress(data []byte) ([]byte, error) {
	return snappy.Encode(nil, data), nil
}

func (s *snappyCompressor) Decompress(data []byte) ([]byte, error) {
	return snappy.Decode(nil, data)
}

// zstdCompressor implements Zstandard compression.
type zstdCompressor struct {
	encoder *zstd.Encoder
	decoder *zstd.Decoder
}

// newZstdCompressor creates a new Zstandard compressor with default settings.
func newZstdCompressor() (*zstdCompressor, error) {
	encoder, err := zstd.NewWriter(nil, zstd.WithEncoderLevel(zstd.SpeedDefault))
	if err != nil {
		return nil, fmt.Errorf("failed to create zstd encoder: %w", err)
	}

	decoder, err := zstd.NewReader(nil)
	if err != nil {
		encoder.Close()
		return nil, fmt.Errorf("failed to create zstd decoder: %w", err)
	}

	return &zstdCompressor{
		encoder: encoder,
		decoder: decoder,
	}, nil
}

func (z *zstdCompressor) Compress(data []byte) ([]byte, error) {
	if z.encoder == nil {
		compressor, err := newZstdCompressor()
		if err != nil {
			return nil, err
		}
		*z = *compressor
	}
	return z.encoder.EncodeAll(data, nil), nil
}

func (z *zstdCompressor) Decompress(data []byte) ([]byte, error) {
	if z.decoder == nil {
		compressor, err := newZstdCompressor()
		if err != nil {
			return nil, err
		}
		*z = *compressor
	}
	return z.decoder.DecodeAll(data, nil)
}

// compress compresses data using the specified compression type.
func compress(compressionType CompressionType, data []byte) ([]byte, error) {
	compressor := NewCompressor(compressionType)
	return compressor.Compress(data)
}

// decompress decompresses data using the specified compression type.
func decompress(compressionType CompressionType, data []byte) ([]byte, error) {
	compressor := NewCompressor(compressionType)
	return compressor.Decompress(data)
}

// CompressWriter wraps an io.Writer with compression.
type CompressWriter struct {
	w          io.Writer
	compressor Compressor
}

// NewCompressWriter creates a new CompressWriter.
func NewCompressWriter(w io.Writer, compressionType CompressionType) *CompressWriter {
	return &CompressWriter{
		w:          w,
		compressor: NewCompressor(compressionType),
	}
}

// Write compresses and writes data.
func (cw *CompressWriter) Write(p []byte) (n int, err error) {
	compressed, err := cw.compressor.Compress(p)
	if err != nil {
		return 0, err
	}
	written, err := cw.w.Write(compressed)
	if err != nil {
		return 0, err
	}
	// Return the original data length, not the compressed length
	return len(p), nil
}

// Close closes the underlying writer if it implements io.Closer.
func (cw *CompressWriter) Close() error {
	if closer, ok := cw.w.(io.Closer); ok {
		return closer.Close()
	}
	return nil
}
