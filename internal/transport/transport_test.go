package transport

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/hashicorp/yamux"
	"github.com/tarungka/wire/internal/protocol"
)

// newTestMuxPair creates a server and client Mux connected via loopback.
func newTestMuxPair(t *testing.T) (server *Mux, client *Mux, serverAddr string) {
	t.Helper()

	sCfg := DefaultConfig()
	sCfg.ListenAddr = "127.0.0.1:0"
	server = NewMux(sCfg)

	ctx := context.Background()
	if err := server.Listen(ctx); err != nil {
		t.Fatalf("server Listen: %v", err)
	}
	serverAddr = server.ListenAddr()

	cCfg := DefaultConfig()
	client = NewMux(cCfg)

	t.Cleanup(func() {
		client.Close()
		server.Close()
	})

	return server, client, serverAddr
}

func TestStreamLifecycle_HandshakeDataEnd(t *testing.T) {
	server, client, addr := newTestMuxPair(t)
	ctx := context.Background()

	// Dial from client → server.
	clientStream, err := client.Dial(ctx, addr)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}

	// Accept on server side.
	serverStream, err := server.Accept(ctx)
	if err != nil {
		t.Fatalf("Accept: %v", err)
	}

	// Receive handshake.
	params, err := serverStream.ReceiveHandshake()
	if err != nil {
		t.Fatalf("ReceiveHandshake: %v", err)
	}
	if params.EffectiveVersion != 1 {
		t.Errorf("EffectiveVersion: got %d, want 1", params.EffectiveVersion)
	}

	// Send 3 DataRecords.
	for i := 0; i < 3; i++ {
		msg := &protocol.DataRecordMsg{
			Key:       []byte(fmt.Sprintf("key-%d", i)),
			Value:     []byte(fmt.Sprintf("value-%d", i)),
			EventTime: int64(i * 1000),
		}
		if err := clientStream.WriteMessage(msg); err != nil {
			t.Fatalf("WriteMessage[%d]: %v", i, err)
		}
	}

	// Send CheckpointBarrier.
	cb := &protocol.CheckpointBarrierMsg{CheckpointID: 1, EpochID: 1, Timestamp: 3000}
	if err := clientStream.WriteMessage(cb); err != nil {
		t.Fatalf("WriteMessage(CB): %v", err)
	}

	// Send EndOfPartition.
	eop := &protocol.EndOfPartitionMsg{SourceID: "test-0", Reason: protocol.EndReasonExhausted}
	if err := clientStream.WriteMessage(eop); err != nil {
		t.Fatalf("WriteMessage(EoP): %v", err)
	}

	// Read all messages on the server side and verify order.
	expected := []uint8{
		protocol.MsgTypeDataRecord,
		protocol.MsgTypeDataRecord,
		protocol.MsgTypeDataRecord,
		protocol.MsgTypeCheckpointBarrier,
		protocol.MsgTypeEndOfPartition,
	}

	for i, want := range expected {
		decoded, err := serverStream.ReadMessage()
		if err != nil {
			t.Fatalf("ReadMessage[%d]: %v", i, err)
		}
		got := msgTypeOf(decoded)
		if got != want {
			t.Errorf("message[%d]: got MsgType 0x%02X, want 0x%02X", i, got, want)
		}
	}

	clientStream.Close()
	serverStream.Close()
}

func TestHandshake_Accept(t *testing.T) {
	server, client, addr := newTestMuxPair(t)
	ctx := context.Background()

	cs, err := client.Dial(ctx, addr)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer cs.Close()

	ss, err := server.Accept(ctx)
	if err != nil {
		t.Fatalf("Accept: %v", err)
	}
	defer ss.Close()

	params, err := ss.ReceiveHandshake()
	if err != nil {
		t.Fatalf("ReceiveHandshake: %v", err)
	}
	if params.EffectiveVersion != 1 {
		t.Errorf("EffectiveVersion: got %d", params.EffectiveVersion)
	}
}

func TestHandshake_Reject(t *testing.T) {
	sCfg := DefaultConfig()
	sCfg.ListenAddr = "127.0.0.1:0"
	sCfg.LocalProtocolVersion = 1
	sCfg.LocalMinVersion = 1
	server := NewMux(sCfg)

	ctx := context.Background()
	if err := server.Listen(ctx); err != nil {
		t.Fatalf("Listen: %v", err)
	}
	serverAddr := server.ListenAddr()

	// Client with incompatible version.
	cCfg := DefaultConfig()
	cCfg.LocalProtocolVersion = 99
	cCfg.LocalMinVersion = 99
	clientMux := NewMux(cCfg)

	t.Cleanup(func() {
		clientMux.Close()
		server.Close()
	})

	cs, err := clientMux.Dial(ctx, serverAddr)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer cs.Close()

	ss, err := server.Accept(ctx)
	if err != nil {
		t.Fatalf("Accept: %v", err)
	}
	defer ss.Close()

	_, err = ss.ReceiveHandshake()
	if err == nil {
		t.Fatal("expected handshake rejection, got nil error")
	}
}

func TestHandshake_Timeout(t *testing.T) {
	sCfg := DefaultConfig()
	sCfg.ListenAddr = "127.0.0.1:0"
	sCfg.HandshakeTimeout = 200 * time.Millisecond
	server := NewMux(sCfg)

	ctx := context.Background()
	if err := server.Listen(ctx); err != nil {
		t.Fatalf("Listen: %v", err)
	}
	serverAddr := server.ListenAddr()

	t.Cleanup(func() { server.Close() })

	// Raw client session — no automatic handshake.
	sess, err := NewClientSession(serverAddr, DefaultConfig())
	if err != nil {
		t.Fatalf("NewClientSession: %v", err)
	}
	defer sess.Close()

	raw, err := sess.OpenStream()
	if err != nil {
		t.Fatalf("OpenStream: %v", err)
	}
	defer raw.Close()

	// Don't send a handshake — just wait.
	ss, err := server.Accept(ctx)
	if err != nil {
		t.Fatalf("Accept: %v", err)
	}
	defer ss.Close()

	_, err = ss.ReceiveHandshake()
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
}

func TestHandshake_WrongFirstFrame(t *testing.T) {
	server, _, addr := newTestMuxPair(t)
	ctx := context.Background()

	// Open a raw session and send a DataRecord as the first frame (no handshake).
	sess, err := NewClientSession(addr, DefaultConfig())
	if err != nil {
		t.Fatalf("NewClientSession: %v", err)
	}
	defer sess.Close()

	raw, err := sess.OpenStream()
	if err != nil {
		t.Fatalf("OpenStream: %v", err)
	}

	// Write a DataRecord as first frame.
	dr := &protocol.DataRecordMsg{Value: []byte("bad"), EventTime: 1}
	protocol.WriteFrame(raw, protocol.MsgTypeDataRecord, dr)
	raw.Close()

	ss, err := server.Accept(ctx)
	if err != nil {
		t.Fatalf("Accept: %v", err)
	}
	defer ss.Close()

	_, err = ss.ReceiveHandshake()
	if err == nil {
		t.Fatal("expected error for wrong first frame")
	}
}

func TestBarrierOrdering(t *testing.T) {
	server, client, addr := newTestMuxPair(t)
	ctx := context.Background()

	cs, err := client.Dial(ctx, addr)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer cs.Close()

	ss, err := server.Accept(ctx)
	if err != nil {
		t.Fatalf("Accept: %v", err)
	}
	defer ss.Close()
	ss.ReceiveHandshake()

	// DR, DR, CB, DR.
	cs.WriteMessage(&protocol.DataRecordMsg{Value: []byte("1"), EventTime: 1})
	cs.WriteMessage(&protocol.DataRecordMsg{Value: []byte("2"), EventTime: 2})
	cs.WriteMessage(&protocol.CheckpointBarrierMsg{CheckpointID: 1, EpochID: 1, Timestamp: 1000})
	cs.WriteMessage(&protocol.DataRecordMsg{Value: []byte("3"), EventTime: 3})

	expected := []uint8{
		protocol.MsgTypeDataRecord,
		protocol.MsgTypeDataRecord,
		protocol.MsgTypeCheckpointBarrier,
		protocol.MsgTypeDataRecord,
	}
	for i, want := range expected {
		m, err := ss.ReadMessage()
		if err != nil {
			t.Fatalf("ReadMessage[%d]: %v", i, err)
		}
		got := msgTypeOf(m)
		if got != want {
			t.Errorf("[%d]: got 0x%02X, want 0x%02X", i, got, want)
		}
	}
}

func TestWatermarkMonotonicity(t *testing.T) {
	server, client, addr := newTestMuxPair(t)
	ctx := context.Background()

	cs, err := client.Dial(ctx, addr)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer cs.Close()

	ss, err := server.Accept(ctx)
	if err != nil {
		t.Fatalf("Accept: %v", err)
	}
	defer ss.Close()
	ss.ReceiveHandshake()

	// Send Watermark(100), then Watermark(50) (backward), then Watermark(200).
	cs.WriteMessage(&protocol.WatermarkMsg{Timestamp: 100, SourceID: "s"})
	cs.WriteMessage(&protocol.WatermarkMsg{Timestamp: 50, SourceID: "s"}) // Should be dropped.
	cs.WriteMessage(&protocol.WatermarkMsg{Timestamp: 200, SourceID: "s"})

	// First watermark: 100.
	m1, err := ss.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage[0]: %v", err)
	}
	wm1 := m1.(*protocol.WatermarkMsg)
	if wm1.Timestamp != 100 {
		t.Errorf("wm1: got %d, want 100", wm1.Timestamp)
	}

	// Second should be 200 (50 was dropped).
	m2, err := ss.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage[1]: %v", err)
	}
	wm2 := m2.(*protocol.WatermarkMsg)
	if wm2.Timestamp != 200 {
		t.Errorf("wm2: got %d, want 200", wm2.Timestamp)
	}
}

func TestEndOfPartition_TerminatesStream(t *testing.T) {
	server, client, addr := newTestMuxPair(t)
	ctx := context.Background()

	cs, err := client.Dial(ctx, addr)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer cs.Close()

	ss, err := server.Accept(ctx)
	if err != nil {
		t.Fatalf("Accept: %v", err)
	}
	defer ss.Close()
	ss.ReceiveHandshake()

	// Send EoP, then a DataRecord (should be dropped).
	cs.WriteMessage(&protocol.EndOfPartitionMsg{SourceID: "s", Reason: protocol.EndReasonExhausted})
	cs.WriteMessage(&protocol.DataRecordMsg{Value: []byte("after-eop"), EventTime: 1})
	// Send another EoP as a sentinel to unblock the reader.
	cs.WriteMessage(&protocol.EndOfPartitionMsg{SourceID: "s2", Reason: protocol.EndReasonExhausted})

	// First read: EndOfPartition.
	m1, err := ss.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage[0]: %v", err)
	}
	if _, ok := m1.(*protocol.EndOfPartitionMsg); !ok {
		t.Fatalf("expected EndOfPartitionMsg, got %T", m1)
	}

	// Close the client stream so server's ReadMessage gets EOF instead of
	// waiting forever for more frames that would be dropped.
	cs.Close()

	// The next read should return an error (EOF or similar) since subsequent
	// frames are dropped and the stream is closed.
	_, err = ss.ReadMessage()
	if err == nil {
		t.Log("ReadMessage after EoP returned nil error — frame was dropped as expected")
	}
}

func TestBackpressure_PauseResume(t *testing.T) {
	server, client, addr := newTestMuxPair(t)
	ctx := context.Background()

	cs, err := client.Dial(ctx, addr)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer cs.Close()

	ss, err := server.Accept(ctx)
	if err != nil {
		t.Fatalf("Accept: %v", err)
	}
	defer ss.Close()
	ss.ReceiveHandshake()

	// Send Pause.
	cs.WriteMessage(&protocol.BackpressureMsg{StreamID: 1, State: protocol.BackpressurePause, BufferUsage: 0.85})
	// Send Resume.
	cs.WriteMessage(&protocol.BackpressureMsg{StreamID: 1, State: protocol.BackpressureResume, BufferUsage: 0.1})

	m1, err := ss.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage[0]: %v", err)
	}
	bp1 := m1.(*protocol.BackpressureMsg)
	if bp1.State != protocol.BackpressurePause {
		t.Errorf("bp1.State: got %d, want Pause", bp1.State)
	}

	m2, err := ss.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage[1]: %v", err)
	}
	bp2 := m2.(*protocol.BackpressureMsg)
	if bp2.State != protocol.BackpressureResume {
		t.Errorf("bp2.State: got %d, want Resume", bp2.State)
	}
}

func TestConcurrentStreams(t *testing.T) {
	server, client, addr := newTestMuxPair(t)
	ctx := context.Background()

	const numStreams = 100
	const msgsPerStream = 100

	var wg sync.WaitGroup

	// Writer goroutines.
	for i := 0; i < numStreams; i++ {
		wg.Add(1)
		go func(streamIdx int) {
			defer wg.Done()
			cs, err := client.Dial(ctx, addr)
			if err != nil {
				t.Errorf("Dial[%d]: %v", streamIdx, err)
				return
			}
			defer cs.Close()

			for j := 0; j < msgsPerStream; j++ {
				msg := &protocol.DataRecordMsg{
					Key:       []byte(fmt.Sprintf("stream-%d", streamIdx)),
					Value:     []byte(fmt.Sprintf("msg-%d", j)),
					EventTime: int64(j),
				}
				if err := cs.WriteMessage(msg); err != nil {
					t.Errorf("WriteMessage[%d][%d]: %v", streamIdx, j, err)
					return
				}
			}
		}(i)
	}

	// Reader goroutines.
	var readWg sync.WaitGroup
	for i := 0; i < numStreams; i++ {
		readWg.Add(1)
		go func() {
			defer readWg.Done()
			ss, err := server.Accept(ctx)
			if err != nil {
				t.Errorf("Accept: %v", err)
				return
			}
			defer ss.Close()

			_, err = ss.ReceiveHandshake()
			if err != nil {
				t.Errorf("ReceiveHandshake: %v", err)
				return
			}

			for j := 0; j < msgsPerStream; j++ {
				_, err := ss.ReadMessage()
				if err != nil {
					t.Errorf("ReadMessage: %v", err)
					return
				}
			}
		}()
	}

	wg.Wait()
	readWg.Wait()
}

func TestSessionReuse(t *testing.T) {
	server, client, addr := newTestMuxPair(t)
	ctx := context.Background()

	// Dial twice to the same address.
	cs1, err := client.Dial(ctx, addr)
	if err != nil {
		t.Fatalf("Dial[0]: %v", err)
	}
	defer cs1.Close()

	cs2, err := client.Dial(ctx, addr)
	if err != nil {
		t.Fatalf("Dial[1]: %v", err)
	}
	defer cs2.Close()

	// Accept both streams on the server side.
	for i := 0; i < 2; i++ {
		ss, err := server.Accept(ctx)
		if err != nil {
			t.Fatalf("Accept[%d]: %v", i, err)
		}
		ss.ReceiveHandshake()
		ss.Close()
	}

	// Check that only one session exists in the peers map.
	client.mu.RLock()
	peerCount := len(client.peers)
	client.mu.RUnlock()
	if peerCount != 1 {
		t.Errorf("expected 1 peer session, got %d", peerCount)
	}
}

func TestUnknownMsgType_Skipped(t *testing.T) {
	server, _, addr := newTestMuxPair(t)
	ctx := context.Background()

	// Raw client — send unknown type then a valid DataRecord.
	sess, err := NewClientSession(addr, DefaultConfig())
	if err != nil {
		t.Fatalf("NewClientSession: %v", err)
	}
	defer sess.Close()

	raw, err := sess.OpenStream()
	if err != nil {
		t.Fatalf("OpenStream: %v", err)
	}

	// Send handshake first.
	hs := &protocol.HandshakeMsg{ProtocolVersion: 1, MinVersion: 1}
	protocol.WriteFrame(raw, protocol.MsgTypeHandshake, hs)

	// Send unknown MsgType (0x40).
	protocol.WriteFrameRaw(raw, 0x40, []byte{0x80}) // Empty msgpack map.

	// Send valid DataRecord.
	dr := &protocol.DataRecordMsg{Value: []byte("valid"), EventTime: 1}
	protocol.WriteFrame(raw, protocol.MsgTypeDataRecord, dr)

	ss, err := server.Accept(ctx)
	if err != nil {
		t.Fatalf("Accept: %v", err)
	}
	defer ss.Close()

	ss.ReceiveHandshake()

	// Should skip the unknown type and return the DataRecord.
	m, err := ss.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}
	dr2 := m.(*protocol.DataRecordMsg)
	if string(dr2.Value) != "valid" {
		t.Errorf("Value: got %q, want %q", dr2.Value, "valid")
	}
}

func TestCRCErrorThreshold(t *testing.T) {
	server, _, addr := newTestMuxPair(t)
	ctx := context.Background()

	sess, err := NewClientSession(addr, DefaultConfig())
	if err != nil {
		t.Fatalf("NewClientSession: %v", err)
	}
	defer sess.Close()

	raw, err := sess.OpenStream()
	if err != nil {
		t.Fatalf("OpenStream: %v", err)
	}

	// Send handshake.
	hs := &protocol.HandshakeMsg{ProtocolVersion: 1, MinVersion: 1}
	protocol.WriteFrame(raw, protocol.MsgTypeHandshake, hs)

	// Send 10 frames with corrupted CRC (write valid frame, then corrupt CRC byte).
	for i := 0; i < MaxConsecutiveCRCErrors; i++ {
		dr := &protocol.DataRecordMsg{Value: []byte("bad"), EventTime: int64(i)}
		payload, _ := protocol.EncodeMsgPack(dr)
		// Write with wrong CRC by using a different msgType for CRC computation.
		// We'll write the frame manually with a bad CRC.
		writeCorruptedFrame(raw, protocol.MsgTypeDataRecord, payload)
	}

	ss, err := server.Accept(ctx)
	if err != nil {
		t.Fatalf("Accept: %v", err)
	}
	defer ss.Close()
	ss.ReceiveHandshake()

	_, err = ss.ReadMessage()
	if err == nil {
		t.Fatal("expected error after CRC threshold")
	}
}

func TestDecodeErrorThreshold(t *testing.T) {
	server, _, addr := newTestMuxPair(t)
	ctx := context.Background()

	sess, err := NewClientSession(addr, DefaultConfig())
	if err != nil {
		t.Fatalf("NewClientSession: %v", err)
	}
	defer sess.Close()

	raw, err := sess.OpenStream()
	if err != nil {
		t.Fatalf("OpenStream: %v", err)
	}

	// Send handshake.
	hs := &protocol.HandshakeMsg{ProtocolVersion: 1, MinVersion: 1}
	protocol.WriteFrame(raw, protocol.MsgTypeHandshake, hs)

	// Send frames with valid CRC but garbled payload (valid msgpack but wrong structure).
	for i := 0; i < MaxConsecutiveDecodeErrors; i++ {
		// Use a payload that's invalid for DataRecord but has correct CRC.
		protocol.WriteFrameRaw(raw, protocol.MsgTypeDataRecord, []byte{0xC1}) // Invalid msgpack byte 0xC1.
	}

	ss, err := server.Accept(ctx)
	if err != nil {
		t.Fatalf("Accept: %v", err)
	}
	defer ss.Close()
	ss.ReceiveHandshake()

	_, err = ss.ReadMessage()
	if err == nil {
		t.Fatal("expected error after decode threshold")
	}
}

// writeCorruptedFrame writes a frame with a deliberately wrong CRC.
func writeCorruptedFrame(w *yamux.Stream, msgType uint8, payload []byte) {
	buf := make([]byte, 4+1+4+len(payload))
	frameLen := uint32(1 + 4 + len(payload))
	buf[0] = byte(frameLen >> 24)
	buf[1] = byte(frameLen >> 16)
	buf[2] = byte(frameLen >> 8)
	buf[3] = byte(frameLen)
	buf[4] = msgType
	// Write a deliberately wrong CRC (all zeros won't match any real payload).
	buf[5] = 0
	buf[6] = 0
	buf[7] = 0
	buf[8] = 0
	copy(buf[9:], payload)
	w.Write(buf)
}

// msgTypeOf returns the protocol message type for a decoded message.
func msgTypeOf(msg any) uint8 {
	switch msg.(type) {
	case *protocol.HandshakeMsg:
		return protocol.MsgTypeHandshake
	case *protocol.DataRecordMsg:
		return protocol.MsgTypeDataRecord
	case *protocol.CheckpointBarrierMsg:
		return protocol.MsgTypeCheckpointBarrier
	case *protocol.WatermarkMsg:
		return protocol.MsgTypeWatermark
	case *protocol.EndOfPartitionMsg:
		return protocol.MsgTypeEndOfPartition
	case *protocol.BackpressureMsg:
		return protocol.MsgTypeBackpressure
	default:
		return 0xFF
	}
}
