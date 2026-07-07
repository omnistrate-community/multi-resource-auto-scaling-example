package autoscaler

import (
	"context"
	"testing"
	"time"

	"github.com/omnistrate-community/multi-resource-auto-scaling-example/internal/config"
	"github.com/omnistrate-community/multi-resource-auto-scaling-example/internal/omnistrate_api"
	"github.com/stretchr/testify/assert"
)

func createMultiResourceTestAutoscaler(client omnistrate_api.Client) *Autoscaler {
	return &Autoscaler{
		config: &config.Config{
			TargetResource:             "api",
			TargetResources:            []string{"api", "worker"},
			Steps:                      10,
			WaitForActiveTimeout:       time.Second,
			WaitForActiveCheckInterval: time.Millisecond,
		},
		client: client,
	}
}

func TestScaleTargetsGroupsAddsAndRemoves(t *testing.T) {
	mockClient := new(MockClient)
	autoscaler := createMultiResourceTestAutoscaler(mockClient)
	ctx := context.Background()

	mockClient.On("GetCurrentCapacity", ctx, "api").Return(omnistrate_api.ResourceInstanceCapacity{
		InstanceID:      "instance-1",
		Status:          omnistrate_api.ACTIVE,
		ResourceID:      "r-api",
		ResourceAlias:   "api",
		CurrentCapacity: 2,
	}, nil).Once()
	mockClient.On("GetCurrentCapacity", ctx, "worker").Return(omnistrate_api.ResourceInstanceCapacity{
		InstanceID:      "instance-1",
		Status:          omnistrate_api.ACTIVE,
		ResourceID:      "r-worker",
		ResourceAlias:   "worker",
		CurrentCapacity: 5,
	}, nil).Once()

	mockClient.On("AddCapacities", ctx, []omnistrate_api.ResourceCapacityChange{
		{ResourceAlias: "api", CapacityToBeAdded: 2},
	}).Return(omnistrate_api.ResourceInstances{
		InstanceID: "instance-1",
		Resources:  []omnistrate_api.ResourceInstance{{ResourceAlias: "api", ResourceID: "r-api"}},
	}, nil).Once()

	mockClient.On("RemoveCapacities", ctx, []omnistrate_api.ResourceCapacityChange{
		{ResourceAlias: "worker", CapacityToBeRemoved: 2},
	}).Return(omnistrate_api.ResourceInstances{
		InstanceID: "instance-1",
		Resources:  []omnistrate_api.ResourceInstance{{ResourceAlias: "worker", ResourceID: "r-worker"}},
	}, nil).Once()
	mockClient.On("GetCurrentCapacity", ctx, "api").Return(omnistrate_api.ResourceInstanceCapacity{
		InstanceID:      "instance-1",
		Status:          omnistrate_api.ACTIVE,
		ResourceID:      "r-api",
		ResourceAlias:   "api",
		CurrentCapacity: 4,
	}, nil).Once()
	mockClient.On("GetCurrentCapacity", ctx, "worker").Return(omnistrate_api.ResourceInstanceCapacity{
		InstanceID:      "instance-1",
		Status:          omnistrate_api.ACTIVE,
		ResourceID:      "r-worker",
		ResourceAlias:   "worker",
		CurrentCapacity: 3,
	}, nil).Once()

	err := autoscaler.ScaleTargets(ctx, map[string]int{
		"api":    4,
		"worker": 3,
	})

	assert.NoError(t, err)
	mockClient.AssertExpectations(t)
}

func TestScaleTargetsRepeatsUntilAllTargetsReached(t *testing.T) {
	mockClient := new(MockClient)
	autoscaler := createMultiResourceTestAutoscaler(mockClient)
	autoscaler.config.Steps = 1
	ctx := context.Background()

	mockClient.On("GetCurrentCapacity", ctx, "api").Return(omnistrate_api.ResourceInstanceCapacity{
		Status: omnistrate_api.ACTIVE, ResourceAlias: "api", CurrentCapacity: 1,
	}, nil).Once()
	mockClient.On("GetCurrentCapacity", ctx, "worker").Return(omnistrate_api.ResourceInstanceCapacity{
		Status: omnistrate_api.ACTIVE, ResourceAlias: "worker", CurrentCapacity: 4,
	}, nil).Once()
	mockClient.On("AddCapacities", ctx, []omnistrate_api.ResourceCapacityChange{
		{ResourceAlias: "api", CapacityToBeAdded: 1},
	}).Return(omnistrate_api.ResourceInstances{}, nil).Once()
	mockClient.On("RemoveCapacities", ctx, []omnistrate_api.ResourceCapacityChange{
		{ResourceAlias: "worker", CapacityToBeRemoved: 1},
	}).Return(omnistrate_api.ResourceInstances{}, nil).Once()

	mockClient.On("GetCurrentCapacity", ctx, "api").Return(omnistrate_api.ResourceInstanceCapacity{
		Status: omnistrate_api.ACTIVE, ResourceAlias: "api", CurrentCapacity: 2,
	}, nil).Once()
	mockClient.On("GetCurrentCapacity", ctx, "worker").Return(omnistrate_api.ResourceInstanceCapacity{
		Status: omnistrate_api.ACTIVE, ResourceAlias: "worker", CurrentCapacity: 3,
	}, nil).Once()
	mockClient.On("AddCapacities", ctx, []omnistrate_api.ResourceCapacityChange{
		{ResourceAlias: "api", CapacityToBeAdded: 1},
	}).Return(omnistrate_api.ResourceInstances{}, nil).Once()
	mockClient.On("RemoveCapacities", ctx, []omnistrate_api.ResourceCapacityChange{
		{ResourceAlias: "worker", CapacityToBeRemoved: 1},
	}).Return(omnistrate_api.ResourceInstances{}, nil).Once()

	mockClient.On("GetCurrentCapacity", ctx, "api").Return(omnistrate_api.ResourceInstanceCapacity{
		Status: omnistrate_api.ACTIVE, ResourceAlias: "api", CurrentCapacity: 3,
	}, nil).Once()
	mockClient.On("GetCurrentCapacity", ctx, "worker").Return(omnistrate_api.ResourceInstanceCapacity{
		Status: omnistrate_api.ACTIVE, ResourceAlias: "worker", CurrentCapacity: 2,
	}, nil).Once()

	err := autoscaler.ScaleTargets(ctx, map[string]int{
		"api":    3,
		"worker": 2,
	})

	assert.NoError(t, err)
	mockClient.AssertExpectations(t)
}

func TestScaleTargetsAddsFromZeroReplicaResources(t *testing.T) {
	mockClient := new(MockClient)
	autoscaler := createMultiResourceTestAutoscaler(mockClient)
	ctx := context.Background()

	mockClient.On("GetCurrentCapacity", ctx, "api").Return(omnistrate_api.ResourceInstanceCapacity{
		Status: omnistrate_api.Status("UNKNOWN"), ResourceAlias: "api", CurrentCapacity: 0,
	}, nil).Once()
	mockClient.On("GetCurrentCapacity", ctx, "worker").Return(omnistrate_api.ResourceInstanceCapacity{
		Status: omnistrate_api.Status("DEPLOYING"), ResourceAlias: "worker", CurrentCapacity: 0,
	}, nil).Once()
	mockClient.On("AddCapacities", ctx, []omnistrate_api.ResourceCapacityChange{
		{ResourceAlias: "api", CapacityToBeAdded: 1},
		{ResourceAlias: "worker", CapacityToBeAdded: 1},
	}).Return(omnistrate_api.ResourceInstances{}, nil).Once()
	mockClient.On("GetCurrentCapacity", ctx, "api").Return(omnistrate_api.ResourceInstanceCapacity{
		Status: omnistrate_api.ACTIVE, ResourceAlias: "api", CurrentCapacity: 1,
	}, nil).Once()
	mockClient.On("GetCurrentCapacity", ctx, "worker").Return(omnistrate_api.ResourceInstanceCapacity{
		Status: omnistrate_api.ACTIVE, ResourceAlias: "worker", CurrentCapacity: 1,
	}, nil).Once()

	err := autoscaler.ScaleTargets(ctx, map[string]int{
		"api":    1,
		"worker": 1,
	})

	assert.NoError(t, err)
	mockClient.AssertExpectations(t)
}

func TestScaleTargetsRejectsUnknownResource(t *testing.T) {
	mockClient := new(MockClient)
	autoscaler := createMultiResourceTestAutoscaler(mockClient)

	err := autoscaler.ScaleTargets(context.Background(), map[string]int{
		"unknown": 2,
	})

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "resource unknown is not configured")
}
