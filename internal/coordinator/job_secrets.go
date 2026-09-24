package coordinator

import (
	"fmt"
	"os"

	"github.com/tarungka/wire/internal/rpc"
	"github.com/tarungka/wire/internal/secretconfig"
)

// validateJobSecretReferences validates the coordinator's environment before a
// submission reserves its name or writes metadata. Never replace graph.Config
// with these temporary resolved bytes: descriptors are persisted for recovery.
func validateJobSecretReferences(graph rpc.JobGraph) error {
	for i, op := range graph.Operators {
		configs := [][]byte{op.Config}
		if op.DLQSink != nil {
			configs = append(configs, op.DLQSink.Config)
		}
		for _, config := range configs {
			resolved, err := secretconfig.Resolve(config, os.LookupEnv)
			clear(resolved)
			if err != nil {
				return fmt.Errorf("%w: operator %d secret configuration: %v", ErrInvalidConfig, i, err)
			}
		}
	}
	return nil
}
