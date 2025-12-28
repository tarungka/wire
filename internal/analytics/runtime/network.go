package runtime

import (
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"github.com/tarungka/wire/internal/analytics"
	"github.com/tarungka/wire/internal/tcp"
)

const (
	MuxAnalyticsHeader byte = 3
)

// DataPlane handles P2P streaming of records between nodes.
type DataPlane struct {
	ln     net.Listener
	dialer *tcp.Dialer
	wm     *WorkerManager

	mu          sync.RWMutex
	connections map[string]net.Conn // NodeID -> Conn
}

// NewDataPlane creates a new DataPlane.
func NewDataPlane(ln net.Listener, dialer *tcp.Dialer, wm *WorkerManager) *DataPlane {
	return &DataPlane{
		ln:          ln,
		dialer:      dialer,
		wm:          wm,
		connections: make(map[string]net.Conn),
	}
}

// Start starts the data plane listener.
func (dp *DataPlane) Start() {
	for {
		conn, err := dp.ln.Accept()
		if err != nil {
			return
		}
		go dp.handleConn(conn)
	}
}

func (dp *DataPlane) handleConn(conn net.Conn) {
	defer conn.Close()

	for {
		// Read record from connection
		record, err := dp.readRecord(conn)
		if err != nil {
			if err != io.EOF {
				fmt.Printf("error reading record from network: %v\n", err)
			}
			return
		}

		// Route to local task
		// For now, we assume the record contains the target TaskID in its metadata
		if taskID, ok := record.Metadata["target_task_id"].(string); ok {
			dp.wm.mu.RLock()
			task, exists := dp.wm.tasks[taskID]
			dp.wm.mu.RUnlock()

			if exists {
				task.Input.Emit(record)
			}
		}
	}
}

func (dp *DataPlane) readRecord(r io.Reader) (*analytics.Record, error) {
	var length uint32
	if err := binary.Read(r, binary.BigEndian, &length); err != nil {
		return nil, err
	}

	data := make([]byte, length)
	if _, err := io.ReadFull(r, data); err != nil {
		return nil, err
	}

	// Very basic deserialization for now
	record := &analytics.Record{
		Metadata: make(map[string]interface{}),
	}

	recordType := data[0]
	offset := 1
	if recordType == 1 { // Barrier
		record.CheckpointID = binary.BigEndian.Uint64(data[offset:])
		offset += 8
	}

	record.Timestamp = time.Unix(0, int64(binary.BigEndian.Uint64(data[offset:])))
	offset += 8

	// Remaining is data payload (assuming string for now to test)
	record.Data = string(data[offset:])

	return record, nil
}

// SendRecord sends a record to a remote node.
func (dp *DataPlane) SendRecord(nodeAddr string, targetTaskID string, record *analytics.Record) error {
	dp.mu.RLock()
	conn, ok := dp.connections[nodeAddr]
	dp.mu.RUnlock()

	if !ok {
		var err error
		conn, err = dp.dialer.Dial(nodeAddr, 5*time.Second)
		if err != nil {
			return err
		}
		dp.mu.Lock()
		dp.connections[nodeAddr] = conn
		dp.mu.Unlock()
	}

	// Simple serialization
	var buf []byte
	if record.IsBarrier() {
		buf = append(buf, 1)
		b := make([]byte, 8)
		binary.BigEndian.PutUint64(b, record.CheckpointID)
		buf = append(buf, b...)
	} else {
		buf = append(buf, 0)
	}

	ts := make([]byte, 8)
	binary.BigEndian.PutUint64(ts, uint64(record.Timestamp.UnixNano()))
	buf = append(buf, ts...)

	payload := []byte(fmt.Sprintf("%v", record.Data))
	buf = append(buf, payload...)

	// Write length prefix
	if err := binary.Write(conn, binary.BigEndian, uint32(len(buf))); err != nil {
		return err
	}

	_, err := conn.Write(buf)
	return err
}
