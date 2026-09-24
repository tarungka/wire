package coordinator

import (
	"bytes"
	"fmt"
	"os"
	"strings"

	"github.com/tarungka/wire/internal/rpc"
	"github.com/tarungka/wire/internal/secretconfig"
)

// jobSecretValues is runtime-only. Its keys are unresolved configuration bytes;
// values must never be included in persisted JobMeta or task descriptors.
type jobSecretValues map[string][]byte

func resolveJobSecretReferences(graph rpc.JobGraph) (jobSecretValues, error) {
	// Take one snapshot so repeated references in this submission agree.
	environment := make(map[string]string)
	for _, entry := range os.Environ() {
		name, value, ok := strings.Cut(entry, "=")
		if ok {
			environment[name] = value
		}
	}
	lookup := func(name string) (string, bool) { value, ok := environment[name]; return value, ok }
	values := make(jobSecretValues)
	for i, op := range graph.Operators {
		configs := [][]byte{op.Config}
		if op.DLQSink != nil {
			configs = append(configs, op.DLQSink.Config)
		}
		for _, config := range configs {
			resolved, err := secretconfig.Resolve(config, lookup)
			if err != nil {
				values.clear()
				return nil, fmt.Errorf("%w: operator %d secret configuration: %v", ErrInvalidConfig, i, err)
			}
			if !bytes.Equal(config, resolved) {
				values[string(config)] = resolved
			} else {
				clear(resolved)
			}
		}
	}
	return values, nil
}

func (values jobSecretValues) clear() {
	for key, value := range values {
		clear(value)
		delete(values, key)
	}
}

// installJobSecretsLocked stores only process-local values. The caller owns
// values after installation and must not clear it until the job is terminal.
func (c *Coordinator) installJobSecretsLocked(jobID string, values jobSecretValues) {
	if c.jobSecrets == nil {
		c.jobSecrets = make(map[string]jobSecretValues)
	}
	c.jobSecrets[jobID].clear()
	c.jobSecrets[jobID] = values
}

func (c *Coordinator) forgetJobSecretsLocked(jobID string) {
	c.jobSecrets[jobID].clear()
	delete(c.jobSecrets, jobID)
}
