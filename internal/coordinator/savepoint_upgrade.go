package coordinator

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/tarungka/wire/internal/protocol"
	"github.com/tarungka/wire/internal/rpc"
)

// SubmitJobFromSavepoint publishes a single successor and its original archive
// reference atomically. It never restarts a source job or rewrites its manifest.
func (c *Coordinator) SubmitJobFromSavepoint(name string, parallelism int, config []byte, path string) (*JobMeta, error) {
	parts := strings.Split(path, "/")
	if len(parts) != 4 || parts[0] != "jobs" || parts[1] == "" || parts[1] == "." || parts[1] == ".." || parts[2] != "checkpoints" {
		return nil, fmt.Errorf("%w: use the savepoint's returned checkpoint path", ErrInvalidConfig)
	}
	id, err := strconv.ParseUint(parts[3], 10, 64)
	if err != nil || id == 0 || name == "" || parallelism < 1 {
		return nil, fmt.Errorf("%w: invalid savepoint submission", ErrInvalidConfig)
	}
	var graph rpc.JobGraph
	if err := protocol.DecodeMsgPack(config, &graph); err != nil {
		return nil, fmt.Errorf("%w: restore requires a structured job graph", ErrInvalidConfig)
	}
	secrets, err := resolveJobSecretReferences(graph)
	installed := false
	defer func() {
		if !installed {
			secrets.clear()
		}
	}()
	if err != nil {
		return nil, err
	}
	if err := graph.CheckpointPolicy.Validate(); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidConfig, err)
	}
	if err := graph.RestartPolicy.Validate(); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidConfig, err)
	}
	now := time.Now().UTC()
	job := &JobMeta{ID: generateJobID(), Name: name, Parallelism: parallelism, Config: append([]byte(nil), config...), Status: JobCreated, CreatedAt: now, UpdatedAt: now, CheckpointPolicy: graph.CheckpointPolicy, RestartPolicy: graph.RestartPolicy}
	targets, err := generateTaskDescriptors(job)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidConfig, err)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.readyLocked() {
		return nil, ErrNotLeader
	}
	if _, exists := c.activeJobNames[name]; exists {
		return nil, ErrJobExists
	}
	source := c.jobs[parts[1]]
	if source == nil {
		return nil, ErrJobNotFound
	}
	if !source.Status.IsTerminal() || source.LatestCheckpoint != id || source.UpgradeSuccessorID != "" {
		return nil, fmt.Errorf("%w: upgrade requires a stopped predecessor's latest boundary with no successor", ErrInvalidTransition)
	}
	raw, err := c.store.Get(CheckpointKey(source.ID, id))
	if err != nil {
		return nil, err
	}
	var cp CheckpointMeta
	if protocol.DecodeMsgPack(raw, &cp) != nil || cp.JobID != source.ID || cp.ID != id || cp.SavepointID == "" || cp.Status != CheckpointCompleted || cp.ManifestVersion == 0 {
		return nil, fmt.Errorf("%w: completed savepoint manifest required", ErrInvalidConfig)
	}
	sp, err := c.GetSavepoint(source.ID, cp.SavepointID)
	if err != nil {
		return nil, err
	}
	if sp.JobID != source.ID || sp.ID != cp.SavepointID || sp.Status != SavepointCompleted || sp.CheckpointID != id || sp.EpochID != cp.EpochID || sp.NumKeyGroups != cp.NumKeyGroups {
		return nil, fmt.Errorf("%w: savepoint identity mismatch", ErrInvalidConfig)
	}
	if _, err := checkpointInventory(c.store, cp); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidConfig, err)
	}
	if err := c.hydrateSavepointChannels(&cp); err != nil {
		return nil, err
	}
	if _, err := planSavepointTaskRestore(cp, targets); err != nil {
		return nil, err
	}
	highest := id
	var scanErr error
	err = c.store.PrefixScan([]byte("jobs/"+source.ID+"/checkpoints/"), func(key, value []byte) bool {
		if strings.HasSuffix(string(key), "/latest") || strings.HasSuffix(string(key), "/metadata.json") {
			return true
		}
		var checkpoint CheckpointMeta
		if scanErr = protocol.DecodeMsgPack(value, &checkpoint); scanErr != nil {
			return false
		}
		highest = max(highest, checkpoint.ID)
		return true
	})
	if err != nil {
		return nil, err
	}
	if scanErr != nil {
		return nil, scanErr
	}
	if highest == ^uint64(0) || source.DeploymentGeneration == ^uint64(0) {
		return nil, fmt.Errorf("%w: predecessor fencing counters exhausted", ErrInvalidConfig)
	}
	job.LatestCheckpoint = id
	job.CheckpointIDFloor = highest
	job.DeploymentGeneration = source.DeploymentGeneration
	job.TransactionJobID = source.TransactionJobID
	if job.TransactionJobID == "" {
		job.TransactionJobID = source.ID
	}
	job.RestoreSavepoint = &SavepointRestoreReference{JobID: source.ID, SavepointID: sp.ID, CheckpointID: id}
	next := *source
	next.UpgradeSuccessorID = job.ID
	jobRaw, err := protocol.EncodeMsgPack(job)
	if err != nil {
		return nil, err
	}
	sourceRaw, err := protocol.EncodeMsgPack(&next)
	if err != nil {
		return nil, err
	}
	if err := c.store.WriteBatch([]KVPair{{Key: JobMetaKey(job.ID), Value: jobRaw}, {Key: JobConfigKey(job.ID), Value: job.Config}, {Key: JobMetaKey(source.ID), Value: sourceRaw}}); err != nil {
		return nil, err
	}
	*source = next
	c.jobs[job.ID] = job
	c.installJobSecretsLocked(job.ID, secrets)
	installed = true
	c.activeJobNames[name] = job.ID
	snapshot := *job
	c.kickScheduler()
	return &snapshot, nil
}

// Earlier assignment records intentionally omitted channel addresses/topology.
// Rebuild channels from the predecessor's graph without altering its saved chain
// or ownership metadata. The latest-boundary guard prevents stale-graph upgrades.
func (c *Coordinator) hydrateSavepointChannels(cp *CheckpointMeta) error {
	source := c.jobs[cp.JobID]
	if source == nil {
		return ErrJobNotFound
	}
	generated, err := generateTaskDescriptors(source)
	if err != nil {
		return err
	}
	channels := make(map[string]rpc.TaskDescriptor, len(generated))
	for _, task := range generated {
		channels[task.TaskID] = task
	}
	for i := range cp.TaskDescriptors {
		saved := &cp.TaskDescriptors[i]
		task, ok := channels[saved.TaskID]
		if !ok {
			return fmt.Errorf("%w: saved graph differs from predecessor", ErrInvalidConfig)
		}
		saved.Upstream, saved.Downstream = task.Upstream, task.Downstream
		saved.OutputGroups, saved.OutputKeyGroups = task.OutputGroups, task.OutputKeyGroups
	}
	return nil
}
