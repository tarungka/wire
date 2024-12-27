package partitioner

import (
	"fmt"
	"hash/fnv"
)

// The current implementation is really slow
func HashFnv(data []byte) (uint64, error) {
	h := fnv.New64a()
	_, err := h.Write([]byte(fmt.Sprintf("%v", data)))
	if err != nil {
		return 0, err
	}
	return h.Sum64(), nil
}

// TODO: implement consistent hashing algorithm
