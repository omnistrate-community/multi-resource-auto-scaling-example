package config

import (
	"reflect"
	"testing"
)

func TestConfigFromEnv_TargetResources(t *testing.T) {
	t.Setenv("AUTOSCALER_TARGET_RESOURCES", "api, worker,ingest")
	t.Setenv("AUTOSCALER_TARGET_RESOURCE", "")

	cfg, err := NewConfigFromEnv()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expected := []string{"api", "worker", "ingest"}
	if !reflect.DeepEqual(cfg.TargetResources, expected) {
		t.Fatalf("expected target resources %v, got %v", expected, cfg.TargetResources)
	}
}

func TestConfigFromEnv_TargetResourceBackwardCompatibility(t *testing.T) {
	t.Setenv("AUTOSCALER_TARGET_RESOURCES", "")
	t.Setenv("AUTOSCALER_TARGET_RESOURCE", "worker")

	cfg, err := NewConfigFromEnv()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expected := []string{"worker"}
	if !reflect.DeepEqual(cfg.TargetResources, expected) {
		t.Fatalf("expected target resources %v, got %v", expected, cfg.TargetResources)
	}
	if cfg.TargetResource != "worker" {
		t.Fatalf("expected legacy target resource worker, got %s", cfg.TargetResource)
	}
}
