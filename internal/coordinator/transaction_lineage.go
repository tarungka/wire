package coordinator

import "strings"

// transactionIdentity keeps an external sink's namespace independent of the
// current physical task name. Older metadata retains its derived namespace.
func transactionIdentity(job *JobMeta, taskID string) (string, string) {
	root := job.TransactionJobID
	if root == "" {
		root = job.ID
	}
	if original := job.TransactionTaskIDs[taskID]; original != "" {
		return root, original
	}
	return root, root + "/" + strings.TrimPrefix(taskID, job.ID+"/")
}

// remapTransactionIdentities composes lineage instead of replacing it with the
// immediate predecessor. The input maps new task IDs to saved task IDs.
func remapTransactionIdentities(source *JobMeta, plan map[string]string) map[string]string {
	result := make(map[string]string, len(plan))
	for target, old := range plan {
		_, result[target] = transactionIdentity(source, old)
	}
	return result
}
