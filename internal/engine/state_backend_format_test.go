package engine

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"hash/crc32"
	"testing"
)

func TestHashMapSnapshotFormatUpgrade(t *testing.T) {
	// Fixed fixtures keep compatibility checks independent of the serializer.
	legacy, err := hex.DecodeString("0101000000010000006b01000000766750d0fa")
	if err != nil {
		t.Fatal(err)
	}
	current, err := hex.DecodeString("574853420101000000010000006b01000000767e6cf0fe")
	if err != nil {
		t.Fatal(err)
	}
	for _, data := range [][]byte{legacy, current} {
		b := NewHashMapStateBackend(0)
		if err := b.Restore(SnapshotHandle{BackendType: StateBackendHashMap, Data: data}); err != nil {
			t.Fatal(err)
		}
		value, err := b.Get([]byte("k"))
		if err != nil || string(value) != "v" {
			t.Fatalf("restored value = %q, %v", value, err)
		}
		snapshot, err := b.Checkpoint(2)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(snapshot.Data, current) {
			t.Fatalf("new encoding = %x", snapshot.Data)
		}
		if err := b.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestHashMapSnapshotHeaderValidation(t *testing.T) {
	encode := func(payload []byte) []byte {
		data := append([]byte(nil), payload...)
		var sum [4]byte
		binary.LittleEndian.PutUint32(sum[:], crc32.ChecksumIEEE(data))
		return append(data, sum[:]...)
	}
	cases := map[string][]byte{
		"invalid magic":       {'X', 'H', 'S', 'B', 1, 0, 0, 0, 0},
		"unsupported version": {'W', 'H', 'S', 'B', 2, 0, 0, 0, 0},
		"truncated header":    {'W', 'H', 'S', 'B', 1},
		"huge count":          {'W', 'H', 'S', 'B', 1, 255, 255, 255, 255},
		"huge key":            {'W', 'H', 'S', 'B', 1, 1, 0, 0, 0, 255, 255, 255, 255, 0, 0, 0, 0},
		"huge value":          {'W', 'H', 'S', 'B', 1, 1, 0, 0, 0, 0, 0, 0, 0, 255, 255, 255, 255},
	}
	for name, payload := range cases {
		t.Run(name, func(t *testing.T) {
			b := NewHashMapStateBackend(0)
			if err := b.Put([]byte("existing"), []byte("kept")); err != nil {
				t.Fatal(err)
			}
			err := b.Restore(SnapshotHandle{BackendType: StateBackendHashMap, Data: encode(payload)})
			if !errors.Is(err, ErrSnapshotCorrupt) {
				t.Fatalf("restore = %v", err)
			}
			got, err := b.Get([]byte("existing"))
			if err != nil || string(got) != "kept" {
				t.Fatal("invalid snapshot changed state")
			}
		})
	}
}
