package protocol

import (
	"bytes"
	"hash/crc32"
	"sync"
	"testing"
)

func BenchmarkWriteFrame_DataRecord_1KB(b *testing.B) {
	msg := &DataRecordMsg{
		Key:       []byte("benchmark-key"),
		Value:     make([]byte, 1024),
		EventTime: 1708819200000,
	}
	var buf bytes.Buffer
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		buf.Reset()
		_ = WriteFrame(&buf, MsgTypeDataRecord, msg)
	}
}

func BenchmarkReadFrame_DataRecord_1KB(b *testing.B) {
	msg := &DataRecordMsg{
		Key:       []byte("benchmark-key"),
		Value:     make([]byte, 1024),
		EventTime: 1708819200000,
	}
	var encoded bytes.Buffer
	_ = WriteFrame(&encoded, MsgTypeDataRecord, msg)
	data := encoded.Bytes()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = ReadFrame(bytes.NewReader(data), DefaultMaxFrameSize)
	}
}

func BenchmarkCRC32C_1KB(b *testing.B) {
	data := make([]byte, 1024)
	b.SetBytes(1024)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		crc32.Checksum(data, crc32cTable)
	}
}

func BenchmarkCRC32C_16MB(b *testing.B) {
	data := make([]byte, 16*1024*1024)
	b.SetBytes(int64(len(data)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		crc32.Checksum(data, crc32cTable)
	}
}

func BenchmarkEncodeMsgPack_DataRecord(b *testing.B) {
	msg := &DataRecordMsg{
		Key:       []byte("key"),
		Value:     make([]byte, 1024),
		EventTime: 1708819200000,
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = EncodeMsgPack(msg)
	}
}

func BenchmarkDecodeMsgPack_DataRecord(b *testing.B) {
	msg := &DataRecordMsg{
		Key:       []byte("key"),
		Value:     make([]byte, 1024),
		EventTime: 1708819200000,
	}
	data, _ := EncodeMsgPack(msg)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var out DataRecordMsg
		_ = DecodeMsgPack(data, &out)
	}
}

func BenchmarkWriteFrame_CheckpointBarrier(b *testing.B) {
	msg := &CheckpointBarrierMsg{
		CheckpointID: 42,
		EpochID:      42,
		Timestamp:    1708819200000,
	}
	var buf bytes.Buffer
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		buf.Reset()
		_ = WriteFrame(&buf, MsgTypeCheckpointBarrier, msg)
	}
}

func BenchmarkReadFrame_Concurrent(b *testing.B) {
	msg := &DataRecordMsg{
		Key:       []byte("key"),
		Value:     make([]byte, 1024),
		EventTime: 1708819200000,
	}
	var encoded bytes.Buffer
	_ = WriteFrame(&encoded, MsgTypeDataRecord, msg)
	data := encoded.Bytes()

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_, _ = ReadFrame(bytes.NewReader(data), DefaultMaxFrameSize)
		}
	})
}

// Ensure crc32cTable is safe for concurrent access.
func BenchmarkCRC32C_Concurrent(b *testing.B) {
	data := make([]byte, 1024)
	b.SetBytes(1024)
	var wg sync.WaitGroup
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			crc32.Checksum(data, crc32cTable)
		}()
	}
	wg.Wait()
}
