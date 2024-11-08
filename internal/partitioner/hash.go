package partitioner

import (
	"fmt"
	"hash/fnv"

	"github.com/tarungka/wire/internal/models"
)

// The current implementation is really slow
func hashFnv(data []byte) (uint64, error) {
	h := fnv.New64a()
	_, err := h.Write([]byte(fmt.Sprintf("%v", data)))
	if err != nil {
		return 0, err
	}
	return h.Sum64(), nil
}


func HashFnv(data *models.Job) (uint64, error) {
	h := fnv.New64a()
	_, err := h.Write([]byte(fmt.Sprintf("%v", data)))
	if err != nil {
		return 0, err
	}
	return h.Sum64(), nil
}

// TODO: implement consistent hashing algorithm
