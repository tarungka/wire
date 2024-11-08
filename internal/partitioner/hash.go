package partitioner

import (
	"fmt"
	"hash/fnv"

	"github.com/tarungka/wire/internal/models"
)

func hashFnv(data []byte) (uint64, error) {
	h := fnv.New64a()
	_, err := h.Write([]byte(fmt.Sprintf("%v", data)))
	if err != nil {
		return 0, err
	}
	return h.Sum64(), nil
}

// The current implementation is really slow
func HashFnv(data *models.Job) (uint64, error) {
	h := fnv.New64a()
	jobData, err := data.GetData() // hashing based on the data in the job to maintain consistency
	if err != nil {
		return 0, err
	}
	_, err = h.Write([]byte(fmt.Sprintf("%v", jobData)))
	if err != nil {
		return 0, err
	}
	return h.Sum64(), nil
}

// TODO: implement consistent hashing algorithm
