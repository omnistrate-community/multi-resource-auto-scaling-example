package omnistrate_api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hashicorp/go-retryablehttp"
	"github.com/omnistrate-community/multi-resource-auto-scaling-example/internal/config"
)

func TestAddCapacitiesSendsGroupedPayload(t *testing.T) {
	var capturedPath string
	var capturedPayload map[string][]map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedPath = r.URL.Path
		if err := json.NewDecoder(r.Body).Decode(&capturedPayload); err != nil {
			t.Fatalf("failed to decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"instanceId":"instance-1","resources":[{"resourceAlias":"api","resourceId":"r-api"},{"resourceAlias":"worker","resourceId":"r-worker"}]}`))
	}))
	defer server.Close()

	client := NewWithHTTPClient(&config.Config{}, retryablehttp.NewClient(), server.URL)

	_, err := client.AddCapacities(context.Background(), []ResourceCapacityChange{
		{ResourceAlias: "api", CapacityToBeAdded: 2},
		{ResourceAlias: "worker", CapacityToBeAdded: 1},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if capturedPath != "/resources/capacity/add" {
		t.Fatalf("expected grouped add path, got %s", capturedPath)
	}
	if len(capturedPayload["resources"]) != 2 {
		t.Fatalf("expected 2 resources, got %d", len(capturedPayload["resources"]))
	}
	if capturedPayload["resources"][0]["resourceAlias"] != "api" || capturedPayload["resources"][0]["capacityToBeAdded"] != float64(2) {
		t.Fatalf("unexpected first resource payload: %#v", capturedPayload["resources"][0])
	}
}

func TestRemoveCapacitiesSendsGroupedPayload(t *testing.T) {
	var capturedPath string
	var capturedPayload map[string][]map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedPath = r.URL.Path
		if err := json.NewDecoder(r.Body).Decode(&capturedPayload); err != nil {
			t.Fatalf("failed to decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"instanceId":"instance-1","resources":[{"resourceAlias":"api","resourceId":"r-api"},{"resourceAlias":"worker","resourceId":"r-worker"}]}`))
	}))
	defer server.Close()

	client := NewWithHTTPClient(&config.Config{}, retryablehttp.NewClient(), server.URL)

	_, err := client.RemoveCapacities(context.Background(), []ResourceCapacityChange{
		{ResourceAlias: "api", CapacityToBeRemoved: 1},
		{ResourceAlias: "worker", CapacityToBeRemoved: 2},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if capturedPath != "/resources/capacity/remove" {
		t.Fatalf("expected grouped remove path, got %s", capturedPath)
	}
	if len(capturedPayload["resources"]) != 2 {
		t.Fatalf("expected 2 resources, got %d", len(capturedPayload["resources"]))
	}
	if capturedPayload["resources"][1]["resourceAlias"] != "worker" || capturedPayload["resources"][1]["capacityToBeRemoved"] != float64(2) {
		t.Fatalf("unexpected second resource payload: %#v", capturedPayload["resources"][1])
	}
}
