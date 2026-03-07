package sdk

import (
	"context"
	"fmt"
)

// clusterExecutor submits pipelines to a remote Wire cluster via RPC.
// Not yet implemented — this is a stub for the cluster execution mode.
type clusterExecutor struct{}

func (ex *clusterExecutor) run(_ context.Context, _ string) (*JobResult, error) {
	return nil, fmt.Errorf("sdk: cluster mode not yet implemented")
}
