package protocol

import (
	"hash/crc32"
	"testing"
)

func TestCRCTypeSeedsMatchWholeMessage(t *testing.T) {
	for _, size := range []int{0, 1, 7, 64, 1024, 65536} {
		message := make([]byte, size+1)
		for i := 1; i < len(message); i++ {
			message[i] = byte(i * 31)
		}
		for mt := 0; mt < 256; mt++ {
			message[0] = byte(mt)
			want := crc32.Checksum(message, crc32cTable)
			if got := computeCRC32C(byte(mt), message[1:]); got != want {
				t.Fatalf("type %d size %d: %x != %x", mt, size, got, want)
			}
		}
	}
}

func BenchmarkCRCFrameStart(b *testing.B) {
	payload := make([]byte, 1024)
	for _, seeded := range []bool{false, true} {
		name := "two_updates"
		if seeded {
			name = "type_seed"
		}
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				mt := byte(i)
				if seeded {
					benchmarkCRC = computeCRC32C(mt, payload)
				} else {
					crc := crc32.Update(0, crc32cTable, []byte{mt})
					benchmarkCRC = crc32.Update(crc, crc32cTable, payload)
				}
			}
		})
	}
}
