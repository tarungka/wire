package coordinator

import (
	"reflect"
	"testing"

	"github.com/tarungka/wire/internal/rpc"
)

func TestReplacementChainInsertionMapping(t *testing.T) {
	old := []rpc.OperatorDescriptor{{OperatorID: "source", Type: rpc.OperatorTypeSource}, {OperatorID: "map", Type: rpc.OperatorTypeMap}, {OperatorID: "sink", Type: rpc.OperatorTypeSink}}
	for _, kind := range []rpc.OperatorType{rpc.OperatorTypeMap, rpc.OperatorTypeFilter, rpc.OperatorTypeFlatMap, rpc.OperatorTypeProcess, rpc.OperatorTypeWindow, rpc.OperatorTypeSource, rpc.OperatorTypeSink} {
		target := append([]rpc.OperatorDescriptor(nil), old[:2]...)
		target = append(target, rpc.OperatorDescriptor{OperatorID: "new", Type: kind}, old[2])
		indexes, err := replacementChainIndexes(old, target)
		valid := kind == rpc.OperatorTypeMap || kind == rpc.OperatorTypeFilter || kind == rpc.OperatorTypeFlatMap
		if (err == nil) != valid {
			t.Fatalf("kind=%v error=%v", kind, err)
		}
		if valid && !reflect.DeepEqual(indexes, []int{0, -1, 1}) {
			t.Fatalf("indexes=%v", indexes)
		}
	}
	for _, bad := range [][]rpc.OperatorDescriptor{{old[1], old[2]}, {old[0], old[2], old[1]}, {old[0], old[1], old[1], old[2]}} {
		if _, err := replacementChainIndexes(old, bad); err == nil {
			t.Fatal("lost, reordered or duplicated operator accepted")
		}
	}
}

func TestReplacementChainRemovalRetainsSnapshotPositions(t *testing.T) {
	old := []rpc.OperatorDescriptor{{OperatorID: "source", Type: rpc.OperatorTypeSource}, {OperatorID: "map", Type: rpc.OperatorTypeMap}, {OperatorID: "extra", Type: rpc.OperatorTypeFilter}, {OperatorID: "sink", Type: rpc.OperatorTypeSink}}
	indexes, err := replacementChainIndexes(old, []rpc.OperatorDescriptor{old[0], old[1], old[3]})
	if err != nil || !reflect.DeepEqual(indexes, []int{0, 2}) {
		t.Fatalf("mapping=%v err=%v", indexes, err)
	}
	old[2].Type = rpc.OperatorTypeWindow
	if _, err := replacementChainIndexes(old, []rpc.OperatorDescriptor{old[0], old[1], old[3]}); err == nil {
		t.Fatal("stateful removal accepted")
	}
}
