package http

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// BenchmarkParseRequestSimple benches the simple-string statement path: an
// array of plain SQL strings, no parameters.
func BenchmarkParseRequestSimple(b *testing.B) {
	for _, n := range []int{1, 10, 100} {
		b.Run(fmt.Sprintf("stmts=%d", n), func(b *testing.B) {
			stmts := make([]string, n)
			for i := range stmts {
				stmts[i] = `INSERT INTO names VALUES(1, 'bob', '123-45-678')`
			}
			body, err := json.Marshal(stmts)
			if err != nil {
				b.Fatal(err)
			}

			b.SetBytes(int64(len(body)))
			b.ReportAllocs()
			b.ResetTimer()

			for i := 0; i < b.N; i++ {
				if _, err := ParseRequest(bytes.NewReader(body)); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkParseRequestParameterized benches the parameterized statement
// path which walks json.Number values and builds command.Parameter objects.
func BenchmarkParseRequestParameterized(b *testing.B) {
	stmts := make([][]interface{}, 100)
	for i := range stmts {
		stmts[i] = []interface{}{
			"INSERT INTO names VALUES(?, ?, ?)",
			map[string]interface{}{"id": i, "name": "bob", "ssn": "123-45-678"},
		}
	}
	body, err := json.Marshal(stmts)
	if err != nil {
		b.Fatal(err)
	}

	b.SetBytes(int64(len(body)))
	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		if _, err := ParseRequest(bytes.NewReader(body)); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkParseRequestLarge benches a single 10KB body, mirroring a
// typical bulk insert payload from a client.
func BenchmarkParseRequestLarge(b *testing.B) {
	var sb strings.Builder
	sb.WriteByte('[')
	for i := 0; i < 200; i++ {
		if i > 0 {
			sb.WriteByte(',')
		}
		fmt.Fprintf(&sb, `"INSERT INTO names VALUES(%d, 'bob-%d', '%d-45-678')"`, i, i, i)
	}
	sb.WriteByte(']')
	body := []byte(sb.String())

	b.SetBytes(int64(len(body)))
	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		if _, err := ParseRequest(bytes.NewReader(body)); err != nil {
			b.Fatal(err)
		}
	}
}
