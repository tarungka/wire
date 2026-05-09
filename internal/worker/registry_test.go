package worker

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/rs/zerolog"

	"github.com/tarungka/wire/internal/engine"
	"github.com/tarungka/wire/internal/rpc"
)

// stubSource is a no-op SourceOperator for registry tests.
type stubSource struct{}

func (*stubSource) Open(_ context.Context) error                        { return nil }
func (*stubSource) Close() error                                        { return nil }
func (*stubSource) Checkpoint(_ uint64) ([]byte, error)                 { return nil, nil }
func (*stubSource) ReadBatch(_ context.Context) ([]engine.Event, error) { return nil, nil }
func (*stubSource) GenerateWatermark() int64                            { return 0 }

type stubMap struct{}

func (*stubMap) Open(_ context.Context) error        { return nil }
func (*stubMap) Close() error                        { return nil }
func (*stubMap) Checkpoint(_ uint64) ([]byte, error) { return nil, nil }
func (*stubMap) Map(_ context.Context, e engine.Event) (engine.Event, error) {
	return e, nil
}

type stubFlatMap struct{}

func (*stubFlatMap) Open(_ context.Context) error        { return nil }
func (*stubFlatMap) Close() error                        { return nil }
func (*stubFlatMap) Checkpoint(_ uint64) ([]byte, error) { return nil, nil }
func (*stubFlatMap) FlatMap(_ context.Context, _ engine.Event, _ func(engine.Event)) error {
	return nil
}

type stubSink struct{}

func (*stubSink) Open(_ context.Context) error                  { return nil }
func (*stubSink) Close() error                                  { return nil }
func (*stubSink) Checkpoint(_ uint64) ([]byte, error)           { return nil, nil }
func (*stubSink) Write(_ context.Context, _ engine.Event) error { return nil }

func newTC() TaskContext { return TaskContext{Log: zerolog.Nop()} }

func TestRegistry_BuildSource(t *testing.T) {
	r := NewRegistry()
	r.RegisterSource("src", func(_ context.Context, _ []byte, _ TaskContext) (engine.SourceOperator, error) {
		return &stubSource{}, nil
	})

	op, err := r.Build(context.Background(), rpc.OperatorDescriptor{
		OperatorID: "op-1",
		Type:       rpc.OperatorTypeSource,
		ClassName:  "src",
	}, newTC())
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if _, ok := op.(engine.SourceOperator); !ok {
		t.Fatalf("Build returned %T, want engine.SourceOperator", op)
	}
}

func TestRegistry_BuildMapAndFlatMapAndSink(t *testing.T) {
	r := NewRegistry()
	r.RegisterMap("m", func(_ context.Context, _ []byte, _ TaskContext) (engine.MapOperator, error) {
		return &stubMap{}, nil
	})
	r.RegisterFlatMap("fm", func(_ context.Context, _ []byte, _ TaskContext) (engine.FlatMapOperator, error) {
		return &stubFlatMap{}, nil
	})
	r.RegisterSink("snk", func(_ context.Context, _ []byte, _ TaskContext) (engine.SinkOperator, error) {
		return &stubSink{}, nil
	})

	cases := []struct {
		desc rpc.OperatorDescriptor
		want any
	}{
		{rpc.OperatorDescriptor{OperatorID: "1", Type: rpc.OperatorTypeMap, ClassName: "m"}, (*stubMap)(nil)},
		{rpc.OperatorDescriptor{OperatorID: "2", Type: rpc.OperatorTypeFilter, ClassName: "m"}, (*stubMap)(nil)},
		{rpc.OperatorDescriptor{OperatorID: "3", Type: rpc.OperatorTypeFlatMap, ClassName: "fm"}, (*stubFlatMap)(nil)},
		{rpc.OperatorDescriptor{OperatorID: "4", Type: rpc.OperatorTypeSink, ClassName: "snk"}, (*stubSink)(nil)},
	}
	for _, c := range cases {
		op, err := r.Build(context.Background(), c.desc, newTC())
		if err != nil {
			t.Fatalf("Build %s/%s: %v", c.desc.Type, c.desc.ClassName, err)
		}
		// Type-check: op should be assignable to the want type.
		switch c.want.(type) {
		case *stubMap:
			if _, ok := op.(*stubMap); !ok {
				t.Fatalf("%s: got %T", c.desc.Type, op)
			}
		case *stubFlatMap:
			if _, ok := op.(*stubFlatMap); !ok {
				t.Fatalf("%s: got %T", c.desc.Type, op)
			}
		case *stubSink:
			if _, ok := op.(*stubSink); !ok {
				t.Fatalf("%s: got %T", c.desc.Type, op)
			}
		}
	}
}

func TestRegistry_MissingClassName(t *testing.T) {
	r := NewRegistry()
	_, err := r.Build(context.Background(), rpc.OperatorDescriptor{
		OperatorID: "x",
		Type:       rpc.OperatorTypeMap,
		ClassName:  "",
	}, newTC())
	if err == nil {
		t.Fatal("expected error for empty ClassName")
	}
	if !strings.Contains(err.Error(), "ClassName") {
		t.Fatalf("error %q should mention ClassName", err)
	}
}

func TestRegistry_UnknownClass(t *testing.T) {
	r := NewRegistry()
	_, err := r.Build(context.Background(), rpc.OperatorDescriptor{
		OperatorID: "x",
		Type:       rpc.OperatorTypeMap,
		ClassName:  "not-registered",
	}, newTC())
	if err == nil {
		t.Fatal("expected error for unknown ClassName")
	}
	if !strings.Contains(err.Error(), "not-registered") {
		t.Fatalf("error %q should name the missing class", err)
	}
}

func TestRegistry_FactoryError(t *testing.T) {
	want := errors.New("boom")
	r := NewRegistry()
	r.RegisterMap("m", func(_ context.Context, _ []byte, _ TaskContext) (engine.MapOperator, error) {
		return nil, want
	})
	_, err := r.Build(context.Background(), rpc.OperatorDescriptor{
		OperatorID: "x",
		Type:       rpc.OperatorTypeMap,
		ClassName:  "m",
	}, newTC())
	if err == nil {
		t.Fatal("expected error from factory")
	}
	if !errors.Is(err, want) {
		t.Fatalf("error %v should wrap %v", err, want)
	}
}

func TestRegistry_DuplicateRegistrationPanics(t *testing.T) {
	r := NewRegistry()
	r.RegisterMap("dup", func(_ context.Context, _ []byte, _ TaskContext) (engine.MapOperator, error) {
		return &stubMap{}, nil
	})

	defer func() {
		if recover() == nil {
			t.Fatal("expected panic on duplicate registration")
		}
	}()
	r.RegisterMap("dup", func(_ context.Context, _ []byte, _ TaskContext) (engine.MapOperator, error) {
		return &stubMap{}, nil
	})
}

func TestRegistry_UnsupportedOperatorType(t *testing.T) {
	r := NewRegistry()
	_, err := r.Build(context.Background(), rpc.OperatorDescriptor{
		OperatorID: "x",
		Type:       rpc.OperatorTypeWindow, // not yet supported
		ClassName:  "anything",
	}, newTC())
	if err == nil {
		t.Fatal("expected error for unsupported operator type")
	}
}

func TestRegistry_ConfigBytesPropagated(t *testing.T) {
	var seen []byte
	r := NewRegistry()
	r.RegisterMap("m", func(_ context.Context, cfg []byte, _ TaskContext) (engine.MapOperator, error) {
		seen = cfg
		return &stubMap{}, nil
	})
	want := []byte("hello-cfg")
	if _, err := r.Build(context.Background(), rpc.OperatorDescriptor{
		Type:      rpc.OperatorTypeMap,
		ClassName: "m",
		Config:    want,
	}, newTC()); err != nil {
		t.Fatalf("Build: %v", err)
	}
	if string(seen) != string(want) {
		t.Fatalf("config bytes: got %q want %q", seen, want)
	}
}
