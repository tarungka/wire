package wal

import (
	"encoding/json"
	"fmt"
)

// jsonMarshaler provides JSON marshaling utilities for WAL types.
type jsonMarshaler struct{}

// Marshal marshals a value to JSON.
func (j jsonMarshaler) Marshal(v interface{}) ([]byte, error) {
	return json.Marshal(v)
}

// Unmarshal unmarshals JSON to a value.
func (j jsonMarshaler) Unmarshal(data []byte, v interface{}) error {
	return json.Unmarshal(data, v)
}

// MarshalIndent marshals a value to indented JSON.
func (j jsonMarshaler) MarshalIndent(v interface{}, prefix, indent string) ([]byte, error) {
	return json.MarshalIndent(v, prefix, indent)
}

// EntryJSON represents the JSON format of a WAL entry.
type EntryJSON struct {
	Offset      int64             `json:"offset"`
	Timestamp   int64             `json:"timestamp"` // Unix nano
	SourceID    string            `json:"source_id"`
	PartitionID int32             `json:"partition_id"`
	Headers     map[string]string `json:"headers,omitempty"`
	Key         []byte            `json:"key,omitempty"`
	Value       []byte            `json:"value"`
	CRC32       uint32            `json:"crc32"`
}

// ToJSON converts an Entry to its JSON representation.
func (e *Entry) ToJSON() ([]byte, error) {
	ej := EntryJSON{
		Offset:      e.Offset,
		Timestamp:   e.Timestamp.UnixNano(),
		SourceID:    e.SourceID,
		PartitionID: e.PartitionID,
		Headers:     e.Headers,
		Key:         e.Key,
		Value:       e.Value,
		CRC32:       e.CRC32,
	}

	marshaler := jsonMarshaler{}
	return marshaler.Marshal(ej)
}

// FromJSON populates an Entry from JSON data.
func (e *Entry) FromJSON(data []byte) error {
	var ej EntryJSON

	marshaler := jsonMarshaler{}
	if err := marshaler.Unmarshal(data, &ej); err != nil {
		return fmt.Errorf("failed to unmarshal entry JSON: %w", err)
	}

	e.Offset = ej.Offset
	e.Timestamp = timeFromUnixNano(ej.Timestamp)
	e.SourceID = ej.SourceID
	e.PartitionID = ej.PartitionID
	e.Headers = ej.Headers
	e.Key = ej.Key
	e.Value = ej.Value
	e.CRC32 = ej.CRC32

	return nil
}

// SegmentInfoJSON represents the JSON format of segment info.
type SegmentInfoJSON struct {
	ID         int64  `json:"id"`
	Path       string `json:"path"`
	Size       int64  `json:"size"`
	StartTime  int64  `json:"start_time"` // Unix nano
	EndTime    int64  `json:"end_time"`   // Unix nano
	EntryCount int64  `json:"entry_count"`
	Corrupted  bool   `json:"corrupted"`
}

// ToJSON converts SegmentInfo to JSON.
func (si *SegmentInfo) ToJSON() ([]byte, error) {
	sij := SegmentInfoJSON{
		ID:         si.ID,
		Path:       si.Path,
		Size:       si.Size,
		StartTime:  si.StartTime.UnixNano(),
		EndTime:    si.EndTime.UnixNano(),
		EntryCount: si.EntryCount,
		Corrupted:  si.Corrupted,
	}

	marshaler := jsonMarshaler{}
	return marshaler.MarshalIndent(sij, "", "  ")
}

// WALStateJSON represents the JSON format of WAL state.
type WALStateJSON struct {
	Directory       string            `json:"directory"`
	WriteOffset     int64             `json:"write_offset"`
	Segments        []SegmentInfoJSON `json:"segments"`
	CurrentSegment  int64             `json:"current_segment_id"`
	Closed          bool              `json:"closed"`
	CompressionType string            `json:"compression_type"`
}

// GetStateJSON returns the current WAL state as JSON.
func (w *WAL) GetStateJSON() ([]byte, error) {
	w.mu.RLock()
	defer w.mu.RUnlock()

	state := WALStateJSON{
		Directory:       w.dir,
		WriteOffset:     w.writeOffset,
		Segments:        make([]SegmentInfoJSON, len(w.segments)),
		Closed:          atomic.LoadInt32(&w.closed) == 1,
		CompressionType: w.compressionType.String(),
	}

	if w.currentSegment != nil {
		state.CurrentSegment = w.currentSegment.id
	}

	for i, seg := range w.segments {
		state.Segments[i] = SegmentInfoJSON{
			ID:         seg.ID,
			Path:       seg.Path,
			Size:       seg.Size,
			StartTime:  seg.StartTime.UnixNano(),
			EndTime:    seg.EndTime.UnixNano(),
			EntryCount: seg.EntryCount,
			Corrupted:  seg.Corrupted,
		}
	}

	marshaler := jsonMarshaler{}
	return marshaler.MarshalIndent(state, "", "  ")
}
