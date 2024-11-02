package utils

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"

	// Using this as it is better maintained
	"github.com/hashicorp/go-msgpack/codec"
)

// FriendlyBytes produces a human readable representation of an SI size
func FriendlyBytes(n uint64) string {
	return Bytes(n)
}

// dirSize returns the total size of all files in the given directory
func DirSize(path string) (int64, error) {
	var size int64
	err := filepath.Walk(path, func(_ string, info os.FileInfo, err error) error {
		if err != nil {
			// If the file doesn't exist, we can ignore it. Snapshot files might
			// disappear during walking.
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if !info.IsDir() {
			size += info.Size()
		}
		return err
	})
	return size, err
}

// ConvertUint64ToBytes converts uint64 to bytes of 64 bits
func ConvertUint64ToBytes(u uint64) []byte {
	buf := make([]byte, 8) // 8*8 = 64
	binary.BigEndian.PutUint64(buf, u)
	return buf
}

// Decode reverses the encode operation on a byte slice input
func DecodeMsgPack(buf []byte, out interface{}) error {
	r := bytes.NewBuffer(buf)
	hd := codec.MsgpackHandle{}
	dec := codec.NewDecoder(r, &hd)
	return dec.Decode(out)
}

// Encode writes an encoded object to a new bytes buffer
func EncodeMsgPack(in interface{}) (*bytes.Buffer, error) {
	buf := bytes.NewBuffer(nil)
	hd := codec.MsgpackHandle{}
	enc := codec.NewEncoder(buf, &hd)
	err := enc.Encode(in)
	return buf, err
}

// Converts bytes to an integer
func ConvertBytesToUint64(b []byte) uint64 {
	return binary.BigEndian.Uint64(b)
}
