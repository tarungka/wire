package keygroup

import (
	"bytes"
	"errors"
	"math/rand/v2"
	"testing"
	"testing/quick"
)

// --- Config validation tests ---

func TestDefaultConfig_Valid(t *testing.T) {
	cfg := DefaultConfig()
	if err := cfg.Validate(); err != nil {
		t.Fatalf("DefaultConfig should be valid: %v", err)
	}
}

func TestConfig_PowerOfTwo(t *testing.T) {
	tests := []struct {
		n    int
		want error
	}{
		{1, nil},
		{2, nil},
		{128, nil},
		{32768, nil},
		{3, ErrNotPowerOfTwo},
		{5, ErrNotPowerOfTwo},
		{100, ErrNotPowerOfTwo},
		{65, ErrNotPowerOfTwo},
	}
	for _, tt := range tests {
		cfg := Config{NumKeyGroups: tt.n, Parallelism: 1}
		err := cfg.Validate()
		if !errors.Is(err, tt.want) {
			t.Errorf("NumKeyGroups=%d: got %v, want %v", tt.n, err, tt.want)
		}
	}
}

func TestConfig_RangeBounds(t *testing.T) {
	tests := []struct {
		n    int
		want error
	}{
		{0, ErrKeyGroupsOutOfRange},
		{-1, ErrKeyGroupsOutOfRange},
		{65536, ErrKeyGroupsOutOfRange},
	}
	for _, tt := range tests {
		cfg := Config{NumKeyGroups: tt.n, Parallelism: 1}
		err := cfg.Validate()
		if !errors.Is(err, tt.want) {
			t.Errorf("NumKeyGroups=%d: got %v, want %v", tt.n, err, tt.want)
		}
	}
}

func TestConfig_ParallelismExceedsKeyGroups(t *testing.T) {
	cfg := Config{NumKeyGroups: 4, Parallelism: 8}
	err := cfg.Validate()
	if !errors.Is(err, ErrParallelismExceedsKeyGroups) {
		t.Fatalf("got %v, want ErrParallelismExceedsKeyGroups", err)
	}
}

func TestConfig_InvalidParallelism(t *testing.T) {
	tests := []struct {
		parallelism int
	}{
		{0},
		{-1},
	}
	for _, tt := range tests {
		cfg := Config{NumKeyGroups: 128, Parallelism: tt.parallelism}
		err := cfg.Validate()
		if !errors.Is(err, ErrInvalidParallelism) {
			t.Errorf("Parallelism=%d: got %v, want ErrInvalidParallelism", tt.parallelism, err)
		}
	}
}

// --- Hash distribution tests ---

func TestKeyGroup_Distribution(t *testing.T) {
	const (
		numKeys      = 1_000_000
		numKeyGroups = 128
		expected     = numKeys / numKeyGroups // 7812
		tolerance    = 0.20                   // 20% per WIP-03 Section 8.1.1
	)

	counts := make([]int, numKeyGroups)
	r := rand.New(rand.NewPCG(42, 0))
	key := make([]byte, 16)
	for range numKeys {
		for i := range key {
			key[i] = byte(r.UintN(256))
		}
		g := KeyGroup(key, numKeyGroups)
		counts[g]++
	}

	expectedF := float64(numKeys) / float64(numKeyGroups)
	lo := int(expectedF * (1.0 - tolerance))
	hi := int(expectedF * (1.0 + tolerance))
	for g, c := range counts {
		if c < lo || c > hi {
			t.Errorf("group %d: count %d outside [%d, %d]", g, c, lo, hi)
		}
	}
}

func TestKeyGroup_Determinism(t *testing.T) {
	f := func(key []byte) bool {
		g1 := KeyGroup(key, 128)
		g2 := KeyGroup(key, 128)
		return g1 == g2
	}
	if err := quick.Check(f, &quick.Config{MaxCount: 10000}); err != nil {
		t.Fatalf("determinism property failed: %v", err)
	}
}

func TestKeyGroup_NilAndEmpty(t *testing.T) {
	g1 := KeyGroup(nil, 128)
	g2 := KeyGroup([]byte{}, 128)
	if g1 != g2 {
		t.Fatalf("nil and empty key should produce the same group: %d vs %d", g1, g2)
	}
	// Verify determinism for nil.
	g3 := KeyGroup(nil, 128)
	if g1 != g3 {
		t.Fatalf("nil key not deterministic: %d vs %d", g1, g3)
	}
}

func TestKeyGroup_GoldenVectors(t *testing.T) {
	// Golden vectors lock murmur3 x86_32 (seed=0) output across library changes.
	// If this test breaks, all persisted key-group assignments become invalid.
	tests := []struct {
		key  []byte
		n    int
		want uint16
	}{
		{nil, 128, 0},
		{[]byte(""), 128, 0},
		{[]byte("hello"), 128, 71},
		{[]byte("world"), 128, 123},
		{[]byte("wire"), 128, 54},
		{[]byte{0x00}, 128, 55},
		{[]byte{0xFF}, 128, 13},
	}
	for _, tt := range tests {
		got := KeyGroup(tt.key, tt.n)
		if got != tt.want {
			t.Errorf("KeyGroup(%q, %d) = %d, want %d (golden vector mismatch — hash compatibility broken)",
				tt.key, tt.n, got, tt.want)
		}
	}
}

// --- Assignment tests ---

func TestAssignment_128Groups4Tasks(t *testing.T) {
	// WIP-03 Section 8.1.2: task 0 = [0,32), task 3 = [96,128).
	r0, err := TaskKeyGroupRange(0, 128, 4)
	if err != nil {
		t.Fatal(err)
	}
	if r0.Start != 0 || r0.End != 32 {
		t.Errorf("task 0: got [%d,%d), want [0,32)", r0.Start, r0.End)
	}

	r3, err := TaskKeyGroupRange(3, 128, 4)
	if err != nil {
		t.Fatal(err)
	}
	if r3.Start != 96 || r3.End != 128 {
		t.Errorf("task 3: got [%d,%d), want [96,128)", r3.Start, r3.End)
	}
}

func TestAssignedTask_RangeConsistency(t *testing.T) {
	// Exhaustive: for every group, AssignedTask returns a task whose range contains the group.
	configs := [][2]int{
		{128, 4},
		{128, 3},
		{256, 7},
		{32768, 100},
		{1, 1},
		{128, 128}, // numKeyGroups == parallelism edge case
	}
	for _, cfg := range configs {
		numKG, par := cfg[0], cfg[1]
		for g := range numKG {
			task := AssignedTask(uint16(g), numKG, par)
			r, err := TaskKeyGroupRange(task, numKG, par)
			if err != nil {
				t.Fatalf("numKG=%d par=%d group=%d: %v", numKG, par, g, err)
			}
			if !r.Contains(uint16(g)) {
				t.Fatalf("numKG=%d par=%d group=%d: assigned task %d range [%d,%d) does not contain group",
					numKG, par, g, task, r.Start, r.End)
			}
		}
	}
}

func TestKeyGroupRange_Contains(t *testing.T) {
	r := KeyGroupRange{Start: 10, End: 20}
	if !r.Contains(10) {
		t.Error("should contain Start")
	}
	if r.Contains(20) {
		t.Error("should not contain End (half-open)")
	}
	if !r.Contains(15) {
		t.Error("should contain mid-range value")
	}
	if r.Contains(9) {
		t.Error("should not contain value below Start")
	}
}

func TestKeyGroupRange_Size(t *testing.T) {
	r := KeyGroupRange{Start: 10, End: 20}
	if r.Size() != 10 {
		t.Errorf("got %d, want 10", r.Size())
	}
}

// --- Rescale tests ---

func TestRescale_4to8(t *testing.T) {
	// WIP-03 Section 8.1.3: scale up 4→8.
	mapping, err := RescaleMapping(128, 4, 8)
	if err != nil {
		t.Fatal(err)
	}

	// New task 0 gets [0,16) from old task 0's [0,32).
	// New task 1 gets [16,32) from old task 0's [0,32).
	// New task 4 gets [64,80) from old task 2's [64,96).
	assertRanges(t, mapping[0], []KeyGroupRange{{0, 16}})
	assertRanges(t, mapping[1], []KeyGroupRange{{16, 32}})
	assertRanges(t, mapping[4], []KeyGroupRange{{64, 80}})
}

func TestRescale_8to4(t *testing.T) {
	// WIP-03 Section 8.1.4: scale down 8→4.
	mapping, err := RescaleMapping(128, 8, 4)
	if err != nil {
		t.Fatal(err)
	}

	// New task 0 gets [0,32) from old tasks 0 ([0,16)) + 1 ([16,32)).
	assertRanges(t, mapping[0], []KeyGroupRange{{0, 16}, {16, 32}})
}

func TestRescale_4to3(t *testing.T) {
	// Uneven split: 128 groups / 3 tasks → sizes 42, 43, 43.
	mapping, err := RescaleMapping(128, 4, 3)
	if err != nil {
		t.Fatal(err)
	}

	// Verify complete coverage.
	totalSize := 0
	for _, ranges := range mapping {
		for _, r := range ranges {
			totalSize += r.Size()
		}
	}
	if totalSize != 128 {
		t.Fatalf("total coverage: got %d, want 128", totalSize)
	}
}

func TestRescale_Identity(t *testing.T) {
	// When oldP == newP, each new task should map to exactly one range matching its own range.
	const numKG, par = 128, 4
	mapping, err := RescaleMapping(numKG, par, par)
	if err != nil {
		t.Fatal(err)
	}
	for i := range par {
		ranges := mapping[i]
		if len(ranges) != 1 {
			t.Fatalf("task %d: got %d ranges, want 1", i, len(ranges))
		}
		expected, err := TaskKeyGroupRange(i, numKG, par)
		if err != nil {
			t.Fatal(err)
		}
		if ranges[0] != expected {
			t.Fatalf("task %d: got %v, want %v", i, ranges[0], expected)
		}
	}
}

func TestRescale_CoverageInvariant(t *testing.T) {
	configs := [][3]int{
		{128, 4, 8},
		{128, 8, 4},
		{128, 4, 3},
		{256, 1, 16},
		{256, 16, 1},
		{32768, 100, 200},
	}
	for _, cfg := range configs {
		numKG, oldP, newP := cfg[0], cfg[1], cfg[2]
		mapping, err := RescaleMapping(numKG, oldP, newP)
		if err != nil {
			t.Fatalf("cfg=%v: %v", cfg, err)
		}

		// Every key group must be covered exactly once.
		covered := make([]bool, numKG)
		for _, ranges := range mapping {
			for _, r := range ranges {
				for g := int(r.Start); g < int(r.End); g++ {
					if covered[g] {
						t.Fatalf("cfg=%v: group %d covered twice", cfg, g)
					}
					covered[g] = true
				}
			}
		}
		for g, c := range covered {
			if !c {
				t.Fatalf("cfg=%v: group %d not covered", cfg, g)
			}
		}
	}
}

// --- Invalid task index tests ---

func TestTaskKeyGroupRange_InvalidIndex(t *testing.T) {
	_, err := TaskKeyGroupRange(-1, 128, 4)
	if !errors.Is(err, ErrInvalidTaskIndex) {
		t.Fatalf("negative index: got %v, want ErrInvalidTaskIndex", err)
	}

	_, err = TaskKeyGroupRange(4, 128, 4)
	if !errors.Is(err, ErrInvalidTaskIndex) {
		t.Fatalf("out of range index: got %v, want ErrInvalidTaskIndex", err)
	}
}

// --- Pebble key tests ---

func TestPebbleKey_Roundtrip(t *testing.T) {
	tests := []struct {
		keyGroup   uint16
		operatorID uint32
		userKey    []byte
		namespace  []byte
	}{
		{0, 0, nil, nil},
		{0, 1, []byte("key"), []byte("ns")},
		{128, 42, []byte("hello"), []byte("world")},
		{32767, 0xFFFFFFFF, []byte{0xFF, 0x00, 0x01}, nil},
		{1, 100, nil, []byte("namespace")}, // nil userKey + non-empty namespace
	}
	for _, tt := range tests {
		encoded := EncodePebbleKey(tt.keyGroup, tt.operatorID, tt.userKey, tt.namespace)
		kg, opID, remainder, err := DecodePebbleKey(encoded)
		if err != nil {
			t.Fatalf("decode error: %v", err)
		}
		if kg != tt.keyGroup {
			t.Errorf("keyGroup: got %d, want %d", kg, tt.keyGroup)
		}
		if opID != tt.operatorID {
			t.Errorf("operatorID: got %d, want %d", opID, tt.operatorID)
		}
		wantRemainder := append(tt.userKey, tt.namespace...)
		if !bytes.Equal(remainder, wantRemainder) {
			t.Errorf("remainder: got %x, want %x", remainder, wantRemainder)
		}
	}
}

func TestPebbleKey_LexicographicOrder(t *testing.T) {
	// Group 0 key must sort before group 1 key regardless of userKey content.
	k0 := EncodePebbleKey(0, 0, []byte{0xFF, 0xFF}, nil)
	k1 := EncodePebbleKey(1, 0, []byte{0x00, 0x00}, nil)
	if bytes.Compare(k0, k1) >= 0 {
		t.Fatalf("group 0 key should sort before group 1 key")
	}

	// Same group, operator 0 before operator 1.
	k2 := EncodePebbleKey(5, 0, []byte("z"), nil)
	k3 := EncodePebbleKey(5, 1, []byte("a"), nil)
	if bytes.Compare(k2, k3) >= 0 {
		t.Fatalf("operator 0 key should sort before operator 1 key within same group")
	}
}

func TestPebbleKey_DecodeTooShort(t *testing.T) {
	_, _, _, err := DecodePebbleKey([]byte{0x00, 0x01, 0x02})
	if !errors.Is(err, ErrPebbleKeyTooShort) {
		t.Fatalf("got %v, want ErrPebbleKeyTooShort", err)
	}
}

func TestPebbleRangeStart(t *testing.T) {
	prefix := PebbleRangeStart(256)
	if len(prefix) != KeyGroupPrefixSize {
		t.Fatalf("expected %d bytes, got %d", KeyGroupPrefixSize, len(prefix))
	}
	// 256 in big-endian = 0x01 0x00
	if prefix[0] != 0x01 || prefix[1] != 0x00 {
		t.Fatalf("got %x, want [01 00]", prefix)
	}
}

// --- Helpers ---

func assertRanges(t *testing.T, got, want []KeyGroupRange) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %d ranges, want %d: %v vs %v", len(got), len(want), got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("range %d: got %v, want %v", i, got[i], want[i])
		}
	}
}
