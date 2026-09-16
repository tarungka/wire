package config

import "testing"

func TestKubernetesHARequiresBudgetsAndAdvertisedEndpoints(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Election.Backend = "kubernetes"
	if err := cfg.Validate(); err == nil {
		t.Fatal("accepted unroutable HA endpoints")
	}
	cfg.HTTP.AdvAddr = "coordinator.example:4001"
	cfg.Node.RPCAdvertiseAddr = "coordinator.example:4002"
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	cfg.Election.Kubernetes.RenewDeadline = cfg.Election.Kubernetes.LeaseDuration
	if err := cfg.Validate(); err == nil {
		t.Fatal("accepted overlapping lease and renewal budgets")
	}
}

func TestWorkerHARequiresDurableEpoch(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Mode = "worker"
	cfg.Worker.CoordinatorSeeds = []string{"coordinator.example:4001"}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	cfg.Worker.EpochPath = ""
	if err := cfg.Validate(); err == nil {
		t.Fatal("accepted HA discovery without persisted fencing")
	}
}
