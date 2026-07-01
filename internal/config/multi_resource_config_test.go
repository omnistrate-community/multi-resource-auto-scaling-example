package config

import (
	"reflect"
	"testing"
)

func TestConfigFromEnv_TargetResources(t *testing.T) {
	t.Setenv("AUTOSCALER_TARGET_RESOURCES", "api, worker,ingest")

	cfg, err := NewConfigFromEnv()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expected := []string{"api", "worker", "ingest"}
	if !reflect.DeepEqual(cfg.TargetResources, expected) {
		t.Fatalf("expected target resources %v, got %v", expected, cfg.TargetResources)
	}
}
