package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"

	"github.com/tarungka/wire/sdk"
)

// fileSource is a bounded example over an immutable file available on every
// worker. It retains at most 8 MiB of input and rejects changed replay data.
type fileSource struct {
	path   string
	lines  [][]byte
	digest string
	next   int
	opened bool
}
type fileOffset struct {
	Version int    `json:"version"`
	Digest  string `json:"sha256"`
	Next    int    `json:"next"`
}

var _ sdk.CheckpointedSource = (*fileSource)(nil)

func (s *fileSource) Open(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.opened {
		return fmt.Errorf("file source already open")
	}
	f, err := os.Open(s.path)
	if err != nil {
		return err
	}
	defer f.Close()
	const limit = 8 * 1024 * 1024
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return err
	}
	if len(data) > limit {
		return fmt.Errorf("example input exceeds 8 MiB")
	}
	var lines [][]byte
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 4096), 1024*1024)
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return err
		}
		lines = append(lines, bytes.Clone(scanner.Bytes()))
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	digest := sha256.Sum256(data)
	s.lines, s.digest, s.next, s.opened = lines, hex.EncodeToString(digest[:]), 0, true
	return nil
}
func (s *fileSource) ReadBatch(ctx context.Context) ([]sdk.Event, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !s.opened {
		return nil, fmt.Errorf("file source not open")
	}
	if s.next == len(s.lines) {
		return nil, nil
	}
	end := min(s.next+32, len(s.lines))
	events := make([]sdk.Event, 0, end-s.next)
	for s.next < end {
		events = append(events, sdk.Event{Key: []byte(strconv.Itoa(s.next)), Value: bytes.Clone(s.lines[s.next])})
		s.next++
	}
	return events, nil
}
func (s *fileSource) Checkpoint(uint64) ([]byte, error) {
	if !s.opened {
		return nil, fmt.Errorf("file source not open")
	}
	return json.Marshal(fileOffset{Version: 1, Digest: s.digest, Next: s.next})
}
func (s *fileSource) RestoreOffset(ctx context.Context, data []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !s.opened {
		return fmt.Errorf("file source must open before restore")
	}
	var offset fileOffset
	if err := json.Unmarshal(data, &offset); err != nil {
		return err
	}
	if offset.Version != 1 || offset.Digest != s.digest || offset.Next < 0 || offset.Next > len(s.lines) {
		return fmt.Errorf("incompatible file checkpoint: version, content or cursor mismatch")
	}
	s.next = offset.Next
	return nil
}
func (*fileSource) GenerateWatermark() int64 { return 0 }
func (s *fileSource) Close() error           { s.lines = nil; s.opened = false; return nil }
