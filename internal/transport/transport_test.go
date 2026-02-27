package transport

import (
	"context"
	"errors"
	"fmt"
	"io"
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

	// First read: EndOfPartition.
	m1, err := ss.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage[0]: %v", err)
	}
	if _, ok := m1.(*protocol.EndOfPartitionMsg); !ok {
		t.Fatalf("expected EndOfPartitionMsg, got %T", m1)
	}

	// Subsequent reads after EOP should return io.EOF immediately
	// instead of spinning in a read loop.
	_, err = ss.ReadMessage()
	if err == nil {
		t.Fatal("expected io.EOF after EndOfPartition, got nil")
	}
	if err.Error() != "EOF" {
		t.Errorf("expected io.EOF, got %v", err)
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

func TestMuxClose_ConcurrentAccept(t *testing.T) {
	// Verify that Mux.Close() with concurrent accepts does not panic
	// (send on closed channel race).
	sCfg := DefaultConfig()
	sCfg.ListenAddr = "127.0.0.1:0"
	server := NewMux(sCfg)

	ctx := context.Background()
	if err := server.Listen(ctx); err != nil {
		t.Fatalf("Listen: %v", err)
	}
	serverAddr := server.ListenAddr()

	cCfg := DefaultConfig()
	client := NewMux(cCfg)

	// Start multiple concurrent dialers to create sessions/streams.
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			cs, err := client.Dial(ctx, serverAddr)
			if err != nil {
				return // Connection may fail during close, that's fine.
			}
			cs.Close()
		}()
	}

	// Start concurrent acceptors.
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ss, err := server.Accept(ctx)
			if err != nil {
				return
			}
			ss.Close()
		}()
	}

	// Give goroutines a moment to start, then close server.
	time.Sleep(50 * time.Millisecond)
	server.Close()
	client.Close()
	wg.Wait()
}

func TestHandshakeTimeout_VsNonTimeout(t *testing.T) {
	// Verify that ReceiveHandshake correctly distinguishes timeout from
	// non-timeout errors (e.g., EOF from a closed connection).
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

	// Case 1: Client closes stream immediately → should NOT get ErrHandshakeTimeout.
	sess, err := NewClientSession(serverAddr, DefaultConfig())
	if err != nil {
		t.Fatalf("NewClientSession: %v", err)
	}
	raw, err := sess.OpenStream()
	if err != nil {
		t.Fatalf("OpenStream: %v", err)
	}
	raw.Close() // Close immediately without sending handshake.

	ss, err := server.Accept(ctx)
	if err != nil {
		t.Fatalf("Accept: %v", err)
	}
	_, err = ss.ReceiveHandshake()
	if err == nil {
		t.Fatal("expected error on closed stream, got nil")
	}
	if errors.Is(err, protocol.ErrHandshakeTimeout) {
		t.Errorf("EOF should not be wrapped as ErrHandshakeTimeout, got: %v", err)
	}
	ss.Close()
	sess.Close()

	// Case 2: No data sent, should timeout → should get ErrHandshakeTimeout.
	sess2, err := NewClientSession(serverAddr, DefaultConfig())
	if err != nil {
		t.Fatalf("NewClientSession: %v", err)
	}
	raw2, err := sess2.OpenStream()
	if err != nil {
		t.Fatalf("OpenStream: %v", err)
	}
	defer raw2.Close()
	defer sess2.Close()

	ss2, err := server.Accept(ctx)
	if err != nil {
		t.Fatalf("Accept: %v", err)
	}
	defer ss2.Close()

	_, err = ss2.ReceiveHandshake()
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
	if !errors.Is(err, protocol.ErrHandshakeTimeout) {
		t.Errorf("expected ErrHandshakeTimeout, got: %v", err)
	}
}

func TestPostEOP_ReturnsEOF(t *testing.T) {
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

	// Send EndOfPartition.
	cs.WriteMessage(&protocol.EndOfPartitionMsg{SourceID: "s", Reason: protocol.EndReasonExhausted})

	// Read the EOP message.
	m1, err := ss.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}
	if _, ok := m1.(*protocol.EndOfPartitionMsg); !ok {
		t.Fatalf("expected EndOfPartitionMsg, got %T", m1)
	}

	// All subsequent reads should return io.EOF.
	for i := 0; i < 3; i++ {
		_, err = ss.ReadMessage()
		if !errors.Is(err, io.EOF) {
			t.Errorf("ReadMessage[%d] after EOP: expected io.EOF, got %v", i, err)
		}
	}
}

func TestWatermarkMonotonicity_MultipleSourceIDs(t *testing.T) {
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

	// Send watermarks from two different sources with independent timelines.
	// Source "a": 100, then 50 (backward — should drop), then 200
	// Source "b": 10, then 20 (forward — should pass)
	cs.WriteMessage(&protocol.WatermarkMsg{Timestamp: 100, SourceID: "a"})
	cs.WriteMessage(&protocol.WatermarkMsg{Timestamp: 10, SourceID: "b"})
	cs.WriteMessage(&protocol.WatermarkMsg{Timestamp: 50, SourceID: "a"})  // backward for "a", drop
	cs.WriteMessage(&protocol.WatermarkMsg{Timestamp: 20, SourceID: "b"})  // forward for "b", pass
	cs.WriteMessage(&protocol.WatermarkMsg{Timestamp: 200, SourceID: "a"}) // forward for "a", pass

	// Expected: a:100, b:10, b:20, a:200 (a:50 dropped)
	expected := []struct {
		sourceID  string
		timestamp int64
	}{
		{"a", 100},
		{"b", 10},
		{"b", 20},
		{"a", 200},
	}

	for i, want := range expected {
		m, err := ss.ReadMessage()
		if err != nil {
			t.Fatalf("ReadMessage[%d]: %v", i, err)
		}
		wm := m.(*protocol.WatermarkMsg)
		if wm.SourceID != want.sourceID || wm.Timestamp != want.timestamp {
			t.Errorf("[%d]: got {%s, %d}, want {%s, %d}",
				i, wm.SourceID, wm.Timestamp, want.sourceID, want.timestamp)
		}
	}
}

func TestDialTimeout_Config(t *testing.T) {
	// Verify that DialTimeout is respected by trying to connect
	// to a non-routable address with a very short timeout.
	cfg := DefaultConfig()
	cfg.DialTimeout = 100 * time.Millisecond

	start := time.Now()
	_, err := NewClientSession("198.51.100.1:4002", cfg) // RFC 5737 TEST-NET-2, non-routable
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected dial error, got nil")
	}
	// Should fail within a reasonable time (not the OS default 2+ minutes).
	if elapsed > 5*time.Second {
		t.Errorf("dial took %v, expected ~100ms timeout", elapsed)
	}
}

func TestErrorCounterResetAfterSuccess_CRC(t *testing.T) {
	// Verify that consecutive CRC error count resets after a valid frame.
	// This ensures intermittent CRC errors don't accumulate across successes
	// and prematurely close the stream.
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
	defer raw.Close()

	// Send handshake.
	protocol.WriteFrame(raw, protocol.MsgTypeHandshake, &protocol.HandshakeMsg{ProtocolVersion: 1, MinVersion: 1})

	// Send (MaxConsecutiveCRCErrors - 1) bad CRC frames.
	payload, _ := protocol.EncodeMsgPack(&protocol.DataRecordMsg{Value: []byte("x"), EventTime: 1})
	for i := 0; i < MaxConsecutiveCRCErrors-1; i++ {
		writeCorruptedFrame(raw, protocol.MsgTypeDataRecord, payload)
	}

	// Send one valid frame — this should reset the counter.
	protocol.WriteFrame(raw, protocol.MsgTypeDataRecord, &protocol.DataRecordMsg{Value: []byte("ok"), EventTime: 2})

	// Send (MaxConsecutiveCRCErrors - 1) more bad CRC frames.
	for i := 0; i < MaxConsecutiveCRCErrors-1; i++ {
		writeCorruptedFrame(raw, protocol.MsgTypeDataRecord, payload)
	}

	// Send another valid frame.
	protocol.WriteFrame(raw, protocol.MsgTypeDataRecord, &protocol.DataRecordMsg{Value: []byte("ok2"), EventTime: 3})

	ss, err := server.Accept(ctx)
	if err != nil {
		t.Fatalf("Accept: %v", err)
	}
	defer ss.Close()
	ss.ReceiveHandshake()

	// First read: should skip 9 bad CRC frames and return "ok".
	m1, err := ss.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage[0]: %v", err)
	}
	if string(m1.(*protocol.DataRecordMsg).Value) != "ok" {
		t.Errorf("got %q, want %q", m1.(*protocol.DataRecordMsg).Value, "ok")
	}

	// Second read: should skip another 9 bad CRC frames and return "ok2".
	// If counters weren't reset, the stream would have closed.
	m2, err := ss.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage[1]: counter was not reset, stream closed prematurely: %v", err)
	}
	if string(m2.(*protocol.DataRecordMsg).Value) != "ok2" {
		t.Errorf("got %q, want %q", m2.(*protocol.DataRecordMsg).Value, "ok2")
	}
}

func TestErrorCounterResetAfterSuccess_Decode(t *testing.T) {
	// Same as CRC test but for decode errors.
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
	defer raw.Close()

	protocol.WriteFrame(raw, protocol.MsgTypeHandshake, &protocol.HandshakeMsg{ProtocolVersion: 1, MinVersion: 1})

	// Send bad decode frame, then good frame.
	protocol.WriteFrameRaw(raw, protocol.MsgTypeDataRecord, []byte{0xC1}) // invalid msgpack
	protocol.WriteFrame(raw, protocol.MsgTypeDataRecord, &protocol.DataRecordMsg{Value: []byte("ok"), EventTime: 1})

	ss, err := server.Accept(ctx)
	if err != nil {
		t.Fatalf("Accept: %v", err)
	}
	defer ss.Close()
	ss.ReceiveHandshake()

	m, err := ss.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage: counter not reset after decode error: %v", err)
	}
	if string(m.(*protocol.DataRecordMsg).Value) != "ok" {
		t.Errorf("got %q, want %q", m.(*protocol.DataRecordMsg).Value, "ok")
	}
}

func TestHandshake_RejectSendsEOPToClient(t *testing.T) {
	// Verify that when server rejects a handshake, it sends an EndOfPartition
	// message with HandshakeSourceID and EndReasonError to the client.
	sCfg := DefaultConfig()
	sCfg.ListenAddr = "127.0.0.1:0"
	sCfg.LocalProtocolVersion = 1
	sCfg.LocalMinVersion = 1
	server := NewMux(sCfg)

	ctx := context.Background()
	if err := server.Listen(ctx); err != nil {
		t.Fatalf("Listen: %v", err)
	}
	t.Cleanup(func() { server.Close() })

	// Raw client to control the handshake content.
	sess, err := NewClientSession(server.ListenAddr(), DefaultConfig())
	if err != nil {
		t.Fatalf("NewClientSession: %v", err)
	}
	defer sess.Close()

	raw, err := sess.OpenStream()
	if err != nil {
		t.Fatalf("OpenStream: %v", err)
	}
	defer raw.Close()

	// Send incompatible handshake.
	protocol.WriteFrame(raw, protocol.MsgTypeHandshake, &protocol.HandshakeMsg{ProtocolVersion: 99, MinVersion: 99})

	ss, err := server.Accept(ctx)
	if err != nil {
		t.Fatalf("Accept: %v", err)
	}
	defer ss.Close()

	_, err = ss.ReceiveHandshake()
	if !errors.Is(err, protocol.ErrVersionIncompatible) {
		t.Fatalf("expected ErrVersionIncompatible, got: %v", err)
	}

	// Client should receive the EOP(Error) frame.
	frame, err := protocol.ReadFrame(raw, DefaultConfig().MaxFrameSize)
	if err != nil {
		t.Fatalf("client read after rejection: %v", err)
	}
	if frame.MsgType != protocol.MsgTypeEndOfPartition {
		t.Fatalf("expected MsgTypeEndOfPartition, got 0x%02X", frame.MsgType)
	}
	decoded, err := protocol.DecodePayload(frame)
	if err != nil {
		t.Fatalf("DecodePayload: %v", err)
	}
	eop := decoded.(*protocol.EndOfPartitionMsg)
	if eop.SourceID != protocol.HandshakeSourceID {
		t.Errorf("SourceID: got %q, want %q", eop.SourceID, protocol.HandshakeSourceID)
	}
	if eop.Reason != protocol.EndReasonError {
		t.Errorf("Reason: got %d, want EndReasonError (%d)", eop.Reason, protocol.EndReasonError)
	}
}

func TestMuxClose_UnblocksPendingAccept(t *testing.T) {
	// Verify that a goroutine blocked on Accept() unblocks when Close() is called.
	sCfg := DefaultConfig()
	sCfg.ListenAddr = "127.0.0.1:0"
	server := NewMux(sCfg)

	if err := server.Listen(context.Background()); err != nil {
		t.Fatalf("Listen: %v", err)
	}

	errCh := make(chan error, 1)
	go func() {
		_, err := server.Accept(context.Background())
		errCh <- err
	}()

	// Give the goroutine time to block on channel read.
	time.Sleep(50 * time.Millisecond)
	server.Close()

	select {
	case err := <-errCh:
		if err == nil {
			t.Fatal("Accept returned nil error after Close")
		}
		// Should get "mux closed" from the channel close.
	case <-time.After(2 * time.Second):
		t.Fatal("Accept did not unblock after Mux.Close()")
	}
}

func TestWriteMessage_AfterEOP_ReturnsError(t *testing.T) {
	// Verify the write-side guard: WriteMessage should fail after EOP is observed.
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

	// Client sends EOP.
	cs.WriteMessage(&protocol.EndOfPartitionMsg{SourceID: "s", Reason: protocol.EndReasonExhausted})

	// Server reads the EOP (sets ended=true on server stream).
	_, err = ss.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}

	// Server-side WriteMessage should now fail since the stream is ended.
	err = ss.WriteMessage(&protocol.DataRecordMsg{Value: []byte("nope"), EventTime: 1})
	if err == nil {
		t.Fatal("expected error writing after EOP, got nil")
	}
}

func TestMuxDial_RetryOnDeadSession(t *testing.T) {
	// Verify that Dial automatically recreates a session when the cached one is dead.
	server, client, addr := newTestMuxPair(t)
	ctx := context.Background()

	// First dial to create and cache a session.
	cs1, err := client.Dial(ctx, addr)
	if err != nil {
		t.Fatalf("Dial[0]: %v", err)
	}
	// Accept + handshake on server side.
	ss1, _ := server.Accept(ctx)
	ss1.ReceiveHandshake()
	cs1.Close()
	ss1.Close()

	// Force-close the cached session to simulate a dead connection.
	client.mu.RLock()
	cachedSess := client.peers[addr]
	client.mu.RUnlock()
	if cachedSess == nil {
		t.Fatal("expected cached session, got nil")
	}
	cachedSess.Close()

	// Dial again — should detect dead session and recreate.
	cs2, err := client.Dial(ctx, addr)
	if err != nil {
		t.Fatalf("Dial[1] retry failed: %v", err)
	}
	defer cs2.Close()

	// Accept on server side to verify the new stream works.
	ss2, err := server.Accept(ctx)
	if err != nil {
		t.Fatalf("Accept[1]: %v", err)
	}
	defer ss2.Close()
	ss2.ReceiveHandshake()

	// Verify data flows on the new stream.
	cs2.WriteMessage(&protocol.DataRecordMsg{Value: []byte("recovered"), EventTime: 1})
	m, err := ss2.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}
	if string(m.(*protocol.DataRecordMsg).Value) != "recovered" {
		t.Errorf("got %q, want %q", m.(*protocol.DataRecordMsg).Value, "recovered")
	}
}

func TestReceiveHandshake_DeadlineClearedAfterSuccess(t *testing.T) {
	// Verify that the read deadline set during handshake is cleared afterward,
	// so a delayed message doesn't hit the old deadline.
	sCfg := DefaultConfig()
	sCfg.ListenAddr = "127.0.0.1:0"
	sCfg.HandshakeTimeout = 50 * time.Millisecond
	server := NewMux(sCfg)

	ctx := context.Background()
	if err := server.Listen(ctx); err != nil {
		t.Fatalf("Listen: %v", err)
	}
	t.Cleanup(func() { server.Close() })

	cCfg := DefaultConfig()
	client := NewMux(cCfg)
	t.Cleanup(func() { client.Close() })

	cs, err := client.Dial(ctx, server.ListenAddr())
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
	if err != nil {
		t.Fatalf("ReceiveHandshake: %v", err)
	}

	// Wait longer than the handshake timeout. If the deadline leaked,
	// the next read would fail with a timeout error.
	time.Sleep(100 * time.Millisecond)

	cs.WriteMessage(&protocol.DataRecordMsg{Value: []byte("delayed"), EventTime: 1})
	m, err := ss.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage after delay should succeed (deadline should be cleared): %v", err)
	}
	if string(m.(*protocol.DataRecordMsg).Value) != "delayed" {
		t.Errorf("got %q, want %q", m.(*protocol.DataRecordMsg).Value, "delayed")
	}
}

func TestMuxAccept_AfterClose_ReturnsError(t *testing.T) {
	// Verify the Accept() API contract: returns error after Close.
	sCfg := DefaultConfig()
	sCfg.ListenAddr = "127.0.0.1:0"
	server := NewMux(sCfg)

	if err := server.Listen(context.Background()); err != nil {
		t.Fatalf("Listen: %v", err)
	}
	server.Close()

	_, err := server.Accept(context.Background())
	if err == nil {
		t.Fatal("Accept after Close should return error")
	}
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
