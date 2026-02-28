package main

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"time"

	"github.com/spf13/pflag"
)

func main() {
	output := pflag.String("output", "testdata.jsonl", "output file path")
	count := pflag.Int("count", 100, "number of records to generate")
	start := pflag.String("start", "2026-01-15T10:00:00Z", "start timestamp (RFC3339)")
	interval := pflag.Duration("interval", 30*time.Second, "time between records")
	pflag.Parse()

	startTime, err := time.Parse(time.RFC3339, *start)
	if err != nil {
		fmt.Fprintf(os.Stderr, "invalid start time: %v\n", err)
		os.Exit(1)
	}

	f, err := os.Create(*output)
	if err != nil {
		fmt.Fprintf(os.Stderr, "create output: %v\n", err)
		os.Exit(1)
	}
	defer f.Close()

	rng := rand.New(rand.NewSource(42))

	for i := 0; i < *count; i++ {
		ts := startTime.Add(time.Duration(i) * *interval)
		rec := map[string]any{
			"timestamp": ts.Format(time.RFC3339),
			"revenue":   100.0 + rng.Float64()*200.0, // [100, 300)
			"profit":    20.0 + rng.Float64()*80.0,   // [20, 100)
		}
		data, _ := json.Marshal(rec)
		fmt.Fprintf(f, "%s\n", data)
	}

	fmt.Printf("Generated %d records to %s\n", *count, *output)
	fmt.Printf("Time span: %s to %s\n",
		startTime.Format(time.RFC3339),
		startTime.Add(time.Duration(*count-1)**interval).Format(time.RFC3339))
}
