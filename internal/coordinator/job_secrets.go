package coordinator

import (
	"bytes"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/tarungka/wire/internal/protocol"
	"github.com/tarungka/wire/internal/rpc"
	"github.com/tarungka/wire/internal/secretconfig"
)

// jobSecretValues is runtime-only. Its keys are unresolved configuration bytes;
// values must never be included in persisted JobMeta or task descriptors.
type resolvedJobConfig struct {
	data    []byte
	secrets []string
}
type jobSecretValues map[string]resolvedJobConfig

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
			resolved, secrets, err := secretconfig.ResolveWithSecrets(config, lookup)
			if err != nil {
				values.clear()
				return nil, fmt.Errorf("%w: operator %d secret configuration: %v", ErrInvalidConfig, i, err)
			}
			if !bytes.Equal(config, resolved) || len(secrets) > 0 {
				if old, ok := values[string(config)]; ok {
					clear(old.data)
					clear(old.secrets)
				}
				values[string(config)] = resolvedJobConfig{data: resolved, secrets: secrets}
			} else {
				clear(resolved)
			}
		}
	}
	return values, nil
}

func (values jobSecretValues) clear() {
	for key, value := range values {
		clear(value.data)
		clear(value.secrets)
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

// ensureJobSecretsLocked reconstructs process-local values after leadership
// recovery. Reference-only metadata deliberately cannot retain the old leader's
// environment; each replacement leader must provision the required variables.
func (c *Coordinator) ensureJobSecretsLocked(job *JobMeta) error {
	if _, ok := c.jobSecrets[job.ID]; ok {
		return nil
	}
	var graph rpc.JobGraph
	if err := protocol.DecodeMsgPack(job.Config, &graph); err != nil {
		// Legacy opaque job configurations have no structured connector settings.
		c.installJobSecretsLocked(job.ID, nil)
		return nil
	}
	values, err := resolveJobSecretReferences(graph)
	if err != nil {
		return err
	}
	c.installJobSecretsLocked(job.ID, values)
	return nil
}

// resolvedTaskCopiesLocked must be used only for immediate secure delivery.
// Even operators without references get independent config bytes: no receiving
// factory or cleanup may mutate metadata or the coordinator's credential cache.
func (c *Coordinator) resolvedTaskCopiesLocked(jobID string, tasks []rpc.TaskDescriptor) []rpc.TaskDescriptor {
	values := c.jobSecrets[jobID]
	result := append([]rpc.TaskDescriptor(nil), tasks...)
	for i := range result {
		sensitive := make(map[string]bool)
		copyConfig := func(raw []byte) []byte {
			if value, ok := values[string(raw)]; ok {
				for _, secret := range value.secrets {
					sensitive[secret] = true
				}
				return bytes.Clone(value.data)
			}
			return bytes.Clone(raw)
		}
		result[i].OperatorChain = append([]rpc.OperatorDescriptor(nil), tasks[i].OperatorChain...)
		for j := range result[i].OperatorChain {
			op := &result[i].OperatorChain[j]
			op.Config = copyConfig(op.Config)
			if op.DLQSink != nil {
				dlq := *op.DLQSink
				dlq.Config = copyConfig(dlq.Config)
				op.DLQSink = &dlq
			}
		}
		result[i].SecretValues = nil
		for secret := range sensitive {
			result[i].SecretValues = append(result[i].SecretValues, secret)
		}
		sort.Strings(result[i].SecretValues)
	}
	return result
}

func (c *Coordinator) tasksNeedSecretsLocked(jobID string, tasks []rpc.TaskDescriptor) bool {
	values := c.jobSecrets[jobID]
	for _, task := range tasks {
		for _, op := range task.OperatorChain {
			if _, ok := values[string(op.Config)]; ok {
				return true
			}
			if op.DLQSink != nil {
				if _, ok := values[string(op.DLQSink.Config)]; ok {
					return true
				}
			}
		}
	}
	return false
}

// Secret payloads never enter the heartbeat/WatchCommands fallback: that queue
// may outlive the authenticated session and be consumed by its replacement.
func (c *Coordinator) secretDeploymentAllowedLocked(workerID string, peer *rpc.Client) bool {
	worker := c.workers[workerID]
	return worker != nil && peer != nil && worker.RPCClient == peer && worker.RPCAuthenticated && worker.SupportsSecretConfig && worker.RPCPeerEpoch == c.epoch && !worker.Lost && !worker.Removed
}
