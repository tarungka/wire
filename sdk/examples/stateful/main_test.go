package main

import (
	"context"
	"reflect"
	"testing"
	"time"
)

func TestStatefulExample(t *testing.T) {
	for _, recovery := range []bool{false, true} {
		name := "bounded"
		if recovery {
			name = "recovery"
		}
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
			defer cancel()
			got, err := run(ctx, recovery)
			if err != nil {
				t.Fatal(err)
			}
			want := output{Main: []string{"customer-a: count=1", "customer-a: count=2"}, Timers: []string{"customer-a: timer=10 count=2"}, SourceAttempts: 1}
			if recovery {
				want.SourceAttempts = 2
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("got %+v, want %+v", got, want)
			}
		})
	}
}
