package command

import (
	"fmt"
	"testing"

	"github.com/tarungka/wire/internal/command/proto"
	pb "google.golang.org/protobuf/proto"
)

func makeQueryRequest(numStmts int) *proto.QueryRequest {
	stmts := make([]*proto.Statement, numStmts)
	for i := range stmts {
		stmts[i] = &proto.Statement{
			Sql: `INSERT INTO "names" VALUES(1,'bob','123-45-678')`,
		}
	}
	return &proto.QueryRequest{
		Request: &proto.Request{
			Statements: stmts,
		},
		Timings:   true,
		Freshness: 100,
	}
}

// BenchmarkProtoMarshal exercises the uncompressed marshal path used for
// small/non-batch requests.
func BenchmarkProtoMarshal(b *testing.B) {
	for _, n := range []int{1, 10, 100} {
		b.Run(fmt.Sprintf("stmts=%d", n), func(b *testing.B) {
			rm := NewRequestMarshaler()
			rm.BatchThreshold = 1_000_000 // never trigger batch compression
			rm.SizeThreshold = 1_000_000  // never trigger size compression
			req := makeQueryRequest(n)

			// Sizing pass for SetBytes.
			out, _, err := rm.Marshal(req)
			if err != nil {
				b.Fatal(err)
			}
			b.SetBytes(int64(len(out)))
			b.ReportAllocs()
			b.ResetTimer()

			for i := 0; i < b.N; i++ {
				if _, _, err := rm.Marshal(req); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkProtoMarshalCompressed forces the gzip path so the benchmark
// captures the compression cost on the hot path.
func BenchmarkProtoMarshalCompressed(b *testing.B) {
	for _, n := range []int{10, 100, 1000} {
		b.Run(fmt.Sprintf("stmts=%d", n), func(b *testing.B) {
			rm := NewRequestMarshaler()
			rm.BatchThreshold = 1
			rm.ForceCompression = true
			req := makeQueryRequest(n)

			out, _, err := rm.Marshal(req)
			if err != nil {
				b.Fatal(err)
			}
			b.SetBytes(int64(len(out)))
			b.ReportAllocs()
			b.ResetTimer()

			for i := 0; i < b.N; i++ {
				if _, _, err := rm.Marshal(req); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkProtoUnmarshalCommand mirrors the wire-level Unmarshal path used
// when a peer receives a Command frame. Captures pure protobuf decode cost.
func BenchmarkProtoUnmarshalCommand(b *testing.B) {
	rm := NewRequestMarshaler()
	rm.BatchThreshold = 1
	req := makeQueryRequest(50)
	sub, comp, err := rm.Marshal(req)
	if err != nil {
		b.Fatal(err)
	}
	cmd := &proto.Command{
		Type:       proto.Command_COMMAND_TYPE_QUERY,
		SubCommand: sub,
		Compressed: comp,
	}
	wire, err := pb.Marshal(cmd)
	if err != nil {
		b.Fatal(err)
	}

	b.SetBytes(int64(len(wire)))
	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		var nc proto.Command
		if err := pb.Unmarshal(wire, &nc); err != nil {
			b.Fatal(err)
		}
	}
}
