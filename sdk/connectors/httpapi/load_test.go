package httpapi

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
)

func TestSourceConcurrentBackpressure(t *testing.T) {
	source, err := NewSource(SourceConfig{Address: "127.0.0.1:0", AllowInsecure: true, BufferSize: 128, MaxBatch: 128})
	if err != nil {
		t.Fatal(err)
	}
	if err = source.Open(t.Context()); err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	var accepted, rejected atomic.Int64
	var wg sync.WaitGroup
	for range 32 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 32 {
				response, err := http.Post("http://"+source.Address()+"/ingest", "application/json", bytes.NewBufferString(`{"events":[{"value":"one"},{"value":"two"}]}`))
				if err != nil {
					t.Error(err)
					return
				}
				_, _ = io.Copy(io.Discard, response.Body)
				_ = response.Body.Close()
				switch response.StatusCode {
				case http.StatusOK:
					accepted.Add(1)
				case http.StatusTooManyRequests:
					rejected.Add(1)
				default:
					t.Errorf("unexpected status %d", response.StatusCode)
				}
			}
		}()
	}
	wg.Wait()
	if accepted.Load() != 64 || rejected.Load() != 960 {
		t.Fatalf("accepted=%d rejected=%d", accepted.Load(), rejected.Load())
	}
	events, err := source.ReadBatch(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 128 {
		t.Fatalf("buffer size: %d", len(events))
	}
	for i := 0; i < len(events); i += 2 {
		if string(events[i].Value) != "one" || string(events[i+1].Value) != "two" {
			t.Fatal("request partially accepted or interleaved")
		}
	}
	if status := post(t, "http://"+source.Address()+"/ingest", `{"events":[{"value":"after drain"}]}`, ""); status != http.StatusOK {
		t.Fatalf("capacity not restored: %d", status)
	}
}

// BenchmarkSourceBackpressure measures HTTP ingest against a deliberately
// saturated queue. It is a bounded-rejection benchmark, not processing throughput.
func BenchmarkSourceBackpressure(b *testing.B) {
	source, err := NewSource(SourceConfig{Address: "127.0.0.1:0", AllowInsecure: true, BufferSize: 128})
	if err != nil {
		b.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err = source.Open(ctx); err != nil {
		b.Fatal(err)
	}
	defer source.Close()
	endpoint := "http://" + source.Address() + "/ingest"
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.MaxIdleConnsPerHost = 32
	transport.MaxConnsPerHost = 32
	client := &http.Client{Transport: transport}
	defer client.CloseIdleConnections()
	var accepted, rejected atomic.Int64
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			response, err := client.Post(endpoint, "application/json", bytes.NewBufferString(`{"events":[{"value":"load"}]}`))
			if err != nil {
				b.Error(err)
				return
			}
			_, _ = io.Copy(io.Discard, response.Body)
			_ = response.Body.Close()
			switch response.StatusCode {
			case http.StatusOK:
				accepted.Add(1)
			case http.StatusTooManyRequests:
				rejected.Add(1)
			default:
				b.Errorf("status %d", response.StatusCode)
			}
		}
	})
	b.StopTimer()
	if b.Failed() {
		return
	}
	if accepted.Load() > 128 || accepted.Load()+rejected.Load() != int64(b.N) {
		b.Fatal("unbounded queue or lost responses")
	}
	b.ReportMetric(float64(rejected.Load())/float64(b.N), "rejected/op")
}
