package encoding

import (
	"fmt"
	"testing"

	"github.com/tarungka/wire/internal/command/proto"
)

func makeQueryRows(rows, cols int) *proto.QueryRows {
	columns := make([]string, cols)
	types := make([]string, cols)
	for c := 0; c < cols; c++ {
		columns[c] = fmt.Sprintf("c%d", c)
		switch c % 3 {
		case 0:
			types[c] = "int"
		case 1:
			types[c] = "float"
		default:
			types[c] = "string"
		}
	}

	values := make([]*proto.Values, rows)
	for r := 0; r < rows; r++ {
		params := make([]*proto.Parameter, cols)
		for c := 0; c < cols; c++ {
			switch c % 3 {
			case 0:
				params[c] = &proto.Parameter{Value: &proto.Parameter_I{I: int64(r*cols + c)}}
			case 1:
				params[c] = &proto.Parameter{Value: &proto.Parameter_D{D: float64(r) + 0.5}}
			default:
				params[c] = &proto.Parameter{Value: &proto.Parameter_S{S: "row-data"}}
			}
		}
		values[r] = &proto.Values{Parameters: params}
	}

	return &proto.QueryRows{
		Columns: columns,
		Types:   types,
		Values:  values,
		Time:    1.234,
	}
}

// BenchmarkJSONMarshalQueryRows captures the cost of serializing QueryRows
// payloads of varying width × depth. This is the HTTP read-path encode cost.
func BenchmarkJSONMarshalQueryRows(b *testing.B) {
	cases := []struct {
		rows, cols int
	}{
		{1, 1},
		{1, 10},
		{100, 10},
		{100, 100},
	}
	for _, tc := range cases {
		b.Run(fmt.Sprintf("rows=%d/cols=%d", tc.rows, tc.cols), func(b *testing.B) {
			enc := Encoder{}
			qr := makeQueryRows(tc.rows, tc.cols)

			out, err := enc.JSONMarshal(qr)
			if err != nil {
				b.Fatal(err)
			}
			b.SetBytes(int64(len(out)))
			b.ReportAllocs()
			b.ResetTimer()

			for i := 0; i < b.N; i++ {
				if _, err := enc.JSONMarshal(qr); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkJSONMarshalQueryRowsAssoc benches the associative shape, since
// associative output materialises a map per row and is meaningfully heavier.
func BenchmarkJSONMarshalQueryRowsAssoc(b *testing.B) {
	enc := Encoder{Associative: true}
	qr := makeQueryRows(100, 10)

	out, err := enc.JSONMarshal(qr)
	if err != nil {
		b.Fatal(err)
	}
	b.SetBytes(int64(len(out)))
	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		if _, err := enc.JSONMarshal(qr); err != nil {
			b.Fatal(err)
		}
	}
}
