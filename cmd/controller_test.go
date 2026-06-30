package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/omnistrate-community/multi-resource-auto-scaling-example/internal/autoscaler"
	"github.com/omnistrate-community/multi-resource-auto-scaling-example/internal/config"
	"github.com/omnistrate-community/multi-resource-auto-scaling-example/internal/omnistrate_api"
)

type controllerTestClient struct {
	mu            sync.Mutex
	capacities    map[string][]omnistrate_api.ResourceInstanceCapacity
	adds          []omnistrate_api.ResourceCapacityChange
	removes       []omnistrate_api.ResourceCapacityChange
	singleAdds    []omnistrate_api.ResourceCapacityChange
	singleRemoves []omnistrate_api.ResourceCapacityChange
}

func (c *controllerTestClient) GetCurrentCapacity(_ context.Context, resourceAlias string) (omnistrate_api.ResourceInstanceCapacity, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	capacities := c.capacities[resourceAlias]
	if len(capacities) == 0 {
		return omnistrate_api.ResourceInstanceCapacity{
			ResourceAlias:   resourceAlias,
			Status:          omnistrate_api.ACTIVE,
			CurrentCapacity: 0,
		}, nil
	}
	capacity := capacities[0]
	if len(capacities) > 1 {
		c.capacities[resourceAlias] = capacities[1:]
	}
	return capacity, nil
}

func (c *controllerTestClient) AddCapacity(_ context.Context, resourceAlias string, capacityToBeAdded uint) (omnistrate_api.ResourceInstance, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.singleAdds = append(c.singleAdds, omnistrate_api.ResourceCapacityChange{
		ResourceAlias:     resourceAlias,
		CapacityToBeAdded: capacityToBeAdded,
	})
	return omnistrate_api.ResourceInstance{ResourceAlias: resourceAlias}, nil
}

func (c *controllerTestClient) RemoveCapacity(_ context.Context, resourceAlias string, capacityToBeRemoved uint) (omnistrate_api.ResourceInstance, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.singleRemoves = append(c.singleRemoves, omnistrate_api.ResourceCapacityChange{
		ResourceAlias:       resourceAlias,
		CapacityToBeRemoved: capacityToBeRemoved,
	})
	return omnistrate_api.ResourceInstance{ResourceAlias: resourceAlias}, nil
}

func (c *controllerTestClient) AddCapacities(_ context.Context, changes []omnistrate_api.ResourceCapacityChange) (omnistrate_api.ResourceInstances, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.adds = append(c.adds, changes...)
	return omnistrate_api.ResourceInstances{Resources: []omnistrate_api.ResourceInstance{}}, nil
}

func (c *controllerTestClient) RemoveCapacities(_ context.Context, changes []omnistrate_api.ResourceCapacityChange) (omnistrate_api.ResourceInstances, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.removes = append(c.removes, changes...)
	return omnistrate_api.ResourceInstances{Resources: []omnistrate_api.ResourceInstance{}}, nil
}

func TestScaleHandlerSingleResourceShorthandRequest(t *testing.T) {
	client := &controllerTestClient{
		capacities: map[string][]omnistrate_api.ResourceInstanceCapacity{
			"worker": {
				{ResourceAlias: "worker", Status: omnistrate_api.ACTIVE, CurrentCapacity: 1},
				{ResourceAlias: "worker", Status: omnistrate_api.ACTIVE, CurrentCapacity: 2},
			},
		},
	}
	autoScaler = autoscaler.NewAutoscalerWithClient(&config.Config{
		TargetResource:             "worker",
		TargetResources:            []string{"worker"},
		Steps:                      10,
		WaitForActiveTimeout:       time.Second,
		WaitForActiveCheckInterval: time.Nanosecond,
	}, client)

	req := httptest.NewRequest(http.MethodPost, "/scale", strings.NewReader(`{"targetCapacity":2}`))
	rec := httptest.NewRecorder()

	scaleHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if len(client.singleAdds) != 1 {
		t.Fatalf("expected one single-resource add call, got %d", len(client.singleAdds))
	}
	if client.singleAdds[0].ResourceAlias != "worker" || client.singleAdds[0].CapacityToBeAdded != 1 {
		t.Fatalf("unexpected single-resource add: %#v", client.singleAdds[0])
	}
}

func TestScaleHandlerMultiResourceRequest(t *testing.T) {
	client := &controllerTestClient{
		capacities: map[string][]omnistrate_api.ResourceInstanceCapacity{
			"api": {
				{ResourceAlias: "api", Status: omnistrate_api.ACTIVE, CurrentCapacity: 1},
				{ResourceAlias: "api", Status: omnistrate_api.ACTIVE, CurrentCapacity: 3},
			},
			"worker": {
				{ResourceAlias: "worker", Status: omnistrate_api.ACTIVE, CurrentCapacity: 4},
				{ResourceAlias: "worker", Status: omnistrate_api.ACTIVE, CurrentCapacity: 2},
			},
		},
	}
	autoScaler = autoscaler.NewAutoscalerWithClient(&config.Config{
		TargetResource:             "api",
		TargetResources:            []string{"api", "worker"},
		Steps:                      10,
		WaitForActiveTimeout:       time.Second,
		WaitForActiveCheckInterval: time.Nanosecond,
	}, client)

	req := httptest.NewRequest(http.MethodPost, "/scale", strings.NewReader(`{"targets":{"api":3,"worker":2}}`))
	rec := httptest.NewRecorder()

	scaleHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if len(client.adds) != 1 {
		t.Fatalf("expected one grouped add change, got %d", len(client.adds))
	}
	if client.adds[0].ResourceAlias != "api" || client.adds[0].CapacityToBeAdded != 2 {
		t.Fatalf("unexpected grouped add: %#v", client.adds[0])
	}
	if len(client.removes) != 1 {
		t.Fatalf("expected one grouped remove change, got %d", len(client.removes))
	}
	if client.removes[0].ResourceAlias != "worker" || client.removes[0].CapacityToBeRemoved != 2 {
		t.Fatalf("unexpected grouped remove: %#v", client.removes[0])
	}

	var response ScaleResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if !response.Success {
		t.Fatalf("expected success response: %#v", response)
	}
}
