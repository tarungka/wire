package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/pflag"
)

func TestMaxFrameSizeFileAndFlagPrecedence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "node.yaml")
	if err := os.WriteFile(path, []byte("max_frame_size: 8192\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{nil, {"--max-frame-size", "4096"}, {"--max-frame-size", "0"}} {
		cfg, err := Load([]string{path})
		if err != nil {
			t.Fatal(err)
		}
		flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
		flags.Uint32("max-frame-size", 16777216, "")
		if err := flags.Parse(args); err != nil {
			t.Fatal(err)
		}
		if err := ApplyFlags(&cfg, flags); err != nil {
			t.Fatal(err)
		}
		want := uint32(8192)
		if len(args) > 0 {
			want = 4096
			if args[1] == "0" {
				want = 0
			}
		}
		if cfg.MaxFrameSize != want {
			t.Fatalf("args=%v limit=%d want=%d", args, cfg.MaxFrameSize, want)
		}
		if err := cfg.Validate(); want == 0 {
			if err == nil || !strings.Contains(err.Error(), "max_frame_size") {
				t.Fatalf("invalid limit accepted: %v", err)
			}
		} else if err != nil {
			t.Fatal(err)
		}
	}
}

func TestCurrentNodeExample(t *testing.T) {
	cfg, err := Load([]string{"../../.config/node.example.yaml"})
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	if cfg.Listen != "127.0.0.1:4002" || cfg.HTTP.Addr != "127.0.0.1:4001" || cfg.MaxFrameSize != 16777216 {
		t.Fatalf("example not applied: %+v", cfg)
	}
}
