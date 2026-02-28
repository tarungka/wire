package keygroup

import "testing"

func BenchmarkKeyGroup_128(b *testing.B) {
	key := []byte("benchmark-key-data")
	for b.Loop() {
		KeyGroup(key, 128)
	}
}

func BenchmarkKeyGroup_32768(b *testing.B) {
	key := []byte("benchmark-key-data")
	for b.Loop() {
		KeyGroup(key, 32768)
	}
}

func BenchmarkAssignedTask(b *testing.B) {
	for i := range b.N {
		AssignedTask(uint16(i%128), 128, 4)
	}
}

func BenchmarkEncodePebbleKey(b *testing.B) {
	userKey := []byte("user-key-data")
	namespace := []byte("namespace")
	for b.Loop() {
		EncodePebbleKey(42, 100, userKey, namespace)
	}
}

func BenchmarkDecodePebbleKey(b *testing.B) {
	encoded := EncodePebbleKey(42, 100, []byte("user-key-data"), []byte("namespace"))
	for b.Loop() {
		DecodePebbleKey(encoded)
	}
}
