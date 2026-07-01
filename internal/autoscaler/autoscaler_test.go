package autoscaler

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4/testutils/require"
	"github.com/omnistrate-community/multi-resource-auto-scaling-example/internal/config"
	"github.com/omnistrate-community/multi-resource-auto-scaling-example/internal/omnistrate_api"
	"github.com/stretchr/testify/assert"
)

// Helper function to create a test autoscaler with mocked client

func createTestAutoscaler(t *testing.T, client omnistrate_api.Client) *Autoscaler {
	// Set required env vars for config
	t.Setenv("AUTOSCALER_COOLDOWN", "0")
	t.Setenv("AUTOSCALER_TARGET_RESOURCES", "test-resource")
	t.Setenv("AUTOSCALER_STEPS", "1")
	t.Setenv("DRY_RUN", "true")
	t.Setenv("AUTOSCALER_WAIT_FOR_ACTIVE_TIMEOUT", "10")
	t.Setenv("AUTOSCALER_WAIT_FOR_ACTIVE_CHECK_INTERVAL", "1") // Set to 1ms to avoid ticker panic on 0 interval
	config, err := config.NewConfigFromEnv()
	if err != nil {
		require.NoError(t, err)
	}
	return &Autoscaler{
		config: config,
		client: client,
	}
}

func TestScaleToTarget_AlreadyAtTarget(t *testing.T) {
	mockClient := new(MockClient)
	autoscaler := createTestAutoscaler(t, mockClient)
	ctx := context.Background()

	// Mock the GetCurrentCapacity call in waitForActiveState to return capacity matching target
	expectedCapacity := omnistrate_api.ResourceInstanceCapacity{
		InstanceID:      "test-instance",
		Status:          omnistrate_api.ACTIVE,
		ResourceID:      "test-resource-id",
		ResourceAlias:   "test-resource",
		CurrentCapacity: 3,
	}
	// waitForActiveState will call GetCurrentCapacity until it gets ACTIVE status
	mockClient.On("GetCurrentCapacity", ctx, "test-resource").Return(expectedCapacity, nil).Once()

	// Call ScaleToTarget with the same capacity
	err := autoscaler.ScaleToTarget(ctx, 3)

	// Assertions
	assert.NoError(t, err)
	mockClient.AssertExpectations(t)
}

func TestScaleToTarget_ScaleUp(t *testing.T) {
	mockClient := new(MockClient)
	autoscaler := createTestAutoscaler(t, mockClient)
	ctx := context.Background()

	// First iteration: waitForActiveState returns capacity = 2, status = ACTIVE
	currentCapacity := omnistrate_api.ResourceInstanceCapacity{
		InstanceID:      "test-instance",
		Status:          omnistrate_api.ACTIVE,
		ResourceID:      "test-resource-id",
		ResourceAlias:   "test-resource",
		CurrentCapacity: 2,
	}
	mockClient.On("GetCurrentCapacity", ctx, "test-resource").Return(currentCapacity, nil).Once()

	// Mock the first AddCapacity call
	expectedInstance := omnistrate_api.ResourceInstance{
		InstanceID:    "test-instance",
		ResourceID:    "test-resource-id",
		ResourceAlias: "test-resource",
	}
	mockClient.On("AddCapacity", ctx, "test-resource", uint(1)).Return(expectedInstance, nil).Once()

	// Second iteration: waitForActiveState checks capacity - now 3
	intermediateCapacity := currentCapacity
	intermediateCapacity.CurrentCapacity = 3
	mockClient.On("GetCurrentCapacity", ctx, "test-resource").Return(intermediateCapacity, nil).Once()

	// Mock the second AddCapacity call
	mockClient.On("AddCapacity", ctx, "test-resource", uint(1)).Return(expectedInstance, nil).Once()

	// Third iteration: waitForActiveState shows capacity is now 4 (target reached, loop exits)
	finalCapacity := currentCapacity
	finalCapacity.CurrentCapacity = 4
	mockClient.On("GetCurrentCapacity", ctx, "test-resource").Return(finalCapacity, nil).Once()

	// Call ScaleToTarget
	err := autoscaler.ScaleToTarget(ctx, 4)

	// Assertions
	assert.NoError(t, err)
	mockClient.AssertExpectations(t)
}

func TestScaleToTarget_ScaleDown(t *testing.T) {
	mockClient := new(MockClient)
	autoscaler := createTestAutoscaler(t, mockClient)
	ctx := context.Background()

	// First iteration: waitForActiveState returns capacity = 5, status = ACTIVE
	currentCapacity := omnistrate_api.ResourceInstanceCapacity{
		InstanceID:      "test-instance",
		Status:          omnistrate_api.ACTIVE,
		ResourceID:      "test-resource-id",
		ResourceAlias:   "test-resource",
		CurrentCapacity: 5,
	}
	mockClient.On("GetCurrentCapacity", ctx, "test-resource").Return(currentCapacity, nil).Once()

	// Mock the first RemoveCapacity call
	expectedInstance := omnistrate_api.ResourceInstance{
		InstanceID:    "test-instance",
		ResourceID:    "test-resource-id",
		ResourceAlias: "test-resource",
	}
	mockClient.On("RemoveCapacity", ctx, "test-resource", uint(1)).Return(expectedInstance, nil).Once()

	// Second iteration: waitForActiveState shows capacity now 4
	intermediateCapacity := currentCapacity
	intermediateCapacity.CurrentCapacity = 4
	mockClient.On("GetCurrentCapacity", ctx, "test-resource").Return(intermediateCapacity, nil).Once()

	// Mock the second RemoveCapacity call
	mockClient.On("RemoveCapacity", ctx, "test-resource", uint(1)).Return(expectedInstance, nil).Once()

	// Third iteration: waitForActiveState shows capacity now 3 (target reached, loop exits)
	finalCapacity := currentCapacity
	finalCapacity.CurrentCapacity = 3
	mockClient.On("GetCurrentCapacity", ctx, "test-resource").Return(finalCapacity, nil).Once()

	// Call ScaleToTarget
	err := autoscaler.ScaleToTarget(ctx, 3)

	// Assertions
	assert.NoError(t, err)
	mockClient.AssertExpectations(t)
}

func TestScaleToTarget_GetCurrentCapacityError(t *testing.T) {
	mockClient := new(MockClient)
	autoscaler := createTestAutoscaler(t, mockClient)
	ctx := context.Background()

	// Mock the GetCurrentCapacity call in waitForActiveState to return an error
	mockClient.On("GetCurrentCapacity", ctx, "test-resource").Return(omnistrate_api.ResourceInstanceCapacity{}, errors.New("API error"))

	// Call ScaleToTarget
	err := autoscaler.ScaleToTarget(ctx, 3)

	// Assertions
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "timeout waiting for instance to become ACTIVE")
	mockClient.AssertExpectations(t)
}

func TestWaitForActiveState_InstanceFailed(t *testing.T) {
	mockClient := new(MockClient)
	autoscaler := createTestAutoscaler(t, mockClient)
	ctx := context.Background()

	// Mock GetCurrentCapacity in waitForActiveState to return FAILED status
	failedCapacity := omnistrate_api.ResourceInstanceCapacity{
		InstanceID:      "test-instance",
		Status:          omnistrate_api.FAILED,
		ResourceID:      "test-resource-id",
		ResourceAlias:   "test-resource",
		CurrentCapacity: 2,
	}
	mockClient.On("GetCurrentCapacity", ctx, "test-resource").Return(failedCapacity, nil).Once()

	// Call ScaleToTarget
	err := autoscaler.ScaleToTarget(ctx, 4)

	// Assertions
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "instance is in FAILED state")
	mockClient.AssertExpectations(t)
}

func TestScaleToTarget_AddCapacityError(t *testing.T) {
	mockClient := new(MockClient)
	autoscaler := createTestAutoscaler(t, mockClient)
	ctx := context.Background()

	// First iteration: waitForActiveState returns capacity = 2, status = ACTIVE
	currentCapacity := omnistrate_api.ResourceInstanceCapacity{
		InstanceID:      "test-instance",
		Status:          omnistrate_api.ACTIVE,
		ResourceID:      "test-resource-id",
		ResourceAlias:   "test-resource",
		CurrentCapacity: 2,
	}
	mockClient.On("GetCurrentCapacity", ctx, "test-resource").Return(currentCapacity, nil).Once()

	// Mock the AddCapacity call to return an error
	mockClient.On("AddCapacity", ctx, "test-resource", uint(1)).Return(omnistrate_api.ResourceInstance{}, errors.New("Add capacity failed"))

	// Call ScaleToTarget
	err := autoscaler.ScaleToTarget(ctx, 3)

	// Assertions
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to scale")
	assert.Contains(t, err.Error(), "Add capacity failed")
	mockClient.AssertExpectations(t)
}

func TestScaleToTarget_RemoveCapacityError(t *testing.T) {
	mockClient := new(MockClient)
	autoscaler := createTestAutoscaler(t, mockClient)
	ctx := context.Background()

	// First iteration: waitForActiveState returns capacity = 4, status = ACTIVE
	currentCapacity := omnistrate_api.ResourceInstanceCapacity{
		InstanceID:      "test-instance",
		Status:          omnistrate_api.ACTIVE,
		ResourceID:      "test-resource-id",
		ResourceAlias:   "test-resource",
		CurrentCapacity: 4,
	}
	mockClient.On("GetCurrentCapacity", ctx, "test-resource").Return(currentCapacity, nil).Once()

	// Mock the RemoveCapacity call to return an error
	mockClient.On("RemoveCapacity", ctx, "test-resource", uint(1)).Return(omnistrate_api.ResourceInstance{}, errors.New("Remove capacity failed"))

	// Call ScaleToTarget
	err := autoscaler.ScaleToTarget(ctx, 3)

	// Assertions
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to scale")
	assert.Contains(t, err.Error(), "Remove capacity failed")
	mockClient.AssertExpectations(t)
}

func TestScaleToTarget_CooldownPeriod(t *testing.T) {
	mockClient := new(MockClient)
	autoscaler := createTestAutoscaler(t, mockClient)
	ctx := context.Background()

	// Set a very short cooldown for testing
	autoscaler.config.CooldownDuration = 10 * time.Millisecond
	autoscaler.lastActionTime = time.Now() // Set last action time to now

	// First iteration: waitForActiveState returns capacity = 2, status = ACTIVE
	currentCapacity := omnistrate_api.ResourceInstanceCapacity{
		InstanceID:      "test-instance",
		Status:          omnistrate_api.ACTIVE,
		ResourceID:      "test-resource-id",
		ResourceAlias:   "test-resource",
		CurrentCapacity: 2,
	}
	mockClient.On("GetCurrentCapacity", ctx, "test-resource").Return(currentCapacity, nil).Once()

	// Mock the AddCapacity call
	expectedInstance := omnistrate_api.ResourceInstance{
		InstanceID:    "test-instance",
		ResourceID:    "test-resource-id",
		ResourceAlias: "test-resource",
	}
	mockClient.On("AddCapacity", ctx, "test-resource", uint(1)).Return(expectedInstance, nil).Once()

	// Second iteration: waitForActiveState shows capacity is now 3 (target reached)
	finalCapacity := currentCapacity
	finalCapacity.CurrentCapacity = 3
	mockClient.On("GetCurrentCapacity", ctx, "test-resource").Return(finalCapacity, nil).Once()

	// Record start time
	startTime := time.Now()

	// Call ScaleToTarget
	err := autoscaler.ScaleToTarget(ctx, 3)

	// Record end time
	endTime := time.Now()

	// Assertions
	assert.NoError(t, err)
	// Should have waited at least the cooldown duration
	assert.True(t, endTime.Sub(startTime) >= autoscaler.config.CooldownDuration)
	mockClient.AssertExpectations(t)
}

func TestGetStatus(t *testing.T) {
	mockClient := new(MockClient)
	autoscaler := createTestAutoscaler(t, mockClient)
	ctx := context.Background()

	// Mock the GetCurrentCapacity call
	expectedCapacity := omnistrate_api.ResourceInstanceCapacity{
		InstanceID:      "test-instance",
		Status:          omnistrate_api.ACTIVE,
		ResourceID:      "test-resource-id",
		ResourceAlias:   "test-resource",
		CurrentCapacity: 3,
	}
	mockClient.On("GetCurrentCapacity", ctx, "test-resource").Return(expectedCapacity, nil)

	// Call GetStatus
	status, err := autoscaler.GetStatus(ctx)

	// Assertions
	assert.NoError(t, err)
	assert.NotNil(t, status)
	assert.Equal(t, expectedCapacity.CurrentCapacity, status.CurrentCapacity)
	assert.Equal(t, expectedCapacity.Status, status.Status)
	assert.False(t, status.ScalingInProgress)
	assert.Equal(t, 0, status.TargetCapacity)
	assert.False(t, status.InCooldownPeriod)
	// Verify new resource metadata fields
	assert.Equal(t, expectedCapacity.InstanceID, status.InstanceID)
	assert.Equal(t, expectedCapacity.ResourceID, status.ResourceID)
	assert.Equal(t, expectedCapacity.ResourceAlias, status.ResourceAlias)
	mockClient.AssertExpectations(t)
}

func TestGetStatus_Error(t *testing.T) {
	mockClient := new(MockClient)
	autoscaler := createTestAutoscaler(t, mockClient)
	ctx := context.Background()

	// Mock the GetCurrentCapacity call to return an error
	mockClient.On("GetCurrentCapacity", ctx, "test-resource").Return(omnistrate_api.ResourceInstanceCapacity{}, errors.New("API error"))

	// Call GetStatus
	status, err := autoscaler.GetStatus(ctx)

	// Assertions
	assert.Error(t, err)
	assert.Nil(t, status)
	assert.Contains(t, err.Error(), "API error")
	mockClient.AssertExpectations(t)
}

func TestGetConfig(t *testing.T) {
	mockClient := new(MockClient)
	autoscaler := createTestAutoscaler(t, mockClient)

	// Call GetConfig
	config := autoscaler.GetConfig()

	// Assertions
	assert.NotNil(t, config)
	assert.Equal(t, "test-resource", config.TargetResource)
	assert.Equal(t, uint(1), config.Steps)
	assert.Equal(t, 0*time.Second, config.CooldownDuration) // Set to 0 via env var
	assert.True(t, config.DryRun)
	assert.Equal(t, 10*time.Second, config.WaitForActiveTimeout)
	assert.Equal(t, 1*time.Second, config.WaitForActiveCheckInterval) // Config parser treats as seconds
}

func TestScaleUp_MultipleSteps(t *testing.T) {
	mockClient := new(MockClient)
	autoscaler := createTestAutoscaler(t, mockClient)
	autoscaler.config.Steps = 2 // Set steps to 2
	ctx := context.Background()

	// First iteration: waitForActiveState returns capacity = 1, status = ACTIVE
	currentCapacity := omnistrate_api.ResourceInstanceCapacity{
		InstanceID:      "test-instance",
		Status:          omnistrate_api.ACTIVE,
		ResourceID:      "test-resource-id",
		ResourceAlias:   "test-resource",
		CurrentCapacity: 1,
	}
	mockClient.On("GetCurrentCapacity", ctx, "test-resource").Return(currentCapacity, nil).Once()

	// Mock the AddCapacity call with steps=2 (should add 2 capacity)
	expectedInstance := omnistrate_api.ResourceInstance{
		InstanceID:    "test-instance",
		ResourceID:    "test-resource-id",
		ResourceAlias: "test-resource",
	}
	mockClient.On("AddCapacity", ctx, "test-resource", uint(2)).Return(expectedInstance, nil).Once()

	// Second iteration: waitForActiveState shows capacity is now 3 (target reached, loop exits)
	finalCapacity := currentCapacity
	finalCapacity.CurrentCapacity = 3
	mockClient.On("GetCurrentCapacity", ctx, "test-resource").Return(finalCapacity, nil).Once()

	// Call ScaleToTarget (need to scale up from 1 to 3)
	err := autoscaler.ScaleToTarget(ctx, 3)

	// Assertions
	assert.NoError(t, err)
	mockClient.AssertExpectations(t)
}

func TestScaleDown_MultipleSteps(t *testing.T) {
	mockClient := new(MockClient)
	autoscaler := createTestAutoscaler(t, mockClient)
	autoscaler.config.Steps = 2 // Set steps to 2
	ctx := context.Background()

	// First iteration: waitForActiveState returns capacity = 5, status = ACTIVE
	currentCapacity := omnistrate_api.ResourceInstanceCapacity{
		InstanceID:      "test-instance",
		Status:          omnistrate_api.ACTIVE,
		ResourceID:      "test-resource-id",
		ResourceAlias:   "test-resource",
		CurrentCapacity: 5,
	}
	mockClient.On("GetCurrentCapacity", ctx, "test-resource").Return(currentCapacity, nil).Once()

	// Mock the RemoveCapacity call with steps=2
	expectedInstance := omnistrate_api.ResourceInstance{
		InstanceID:    "test-instance",
		ResourceID:    "test-resource-id",
		ResourceAlias: "test-resource",
	}
	mockClient.On("RemoveCapacity", ctx, "test-resource", uint(2)).Return(expectedInstance, nil).Once()

	// Second iteration: waitForActiveState shows capacity is now 3 (target reached, loop exits)
	finalCapacity := currentCapacity
	finalCapacity.CurrentCapacity = 3
	mockClient.On("GetCurrentCapacity", ctx, "test-resource").Return(finalCapacity, nil).Once()

	// Call ScaleToTarget (need to scale down by 2)
	err := autoscaler.ScaleToTarget(ctx, 3)

	// Assertions
	assert.NoError(t, err)
	mockClient.AssertExpectations(t)
}

func TestScaleDown_LimitedByCurrentCapacity(t *testing.T) {
	mockClient := new(MockClient)
	autoscaler := createTestAutoscaler(t, mockClient)
	autoscaler.config.Steps = 3 // Set steps to 3, but current capacity is only 2
	ctx := context.Background()

	// First iteration: waitForActiveState returns capacity = 2, status = ACTIVE
	currentCapacity := omnistrate_api.ResourceInstanceCapacity{
		InstanceID:      "test-instance",
		Status:          omnistrate_api.ACTIVE,
		ResourceID:      "test-resource-id",
		ResourceAlias:   "test-resource",
		CurrentCapacity: 2,
	}
	mockClient.On("GetCurrentCapacity", ctx, "test-resource").Return(currentCapacity, nil).Once()

	// Mock the RemoveCapacity call - should only remove 2 (current capacity), not 3 (steps)
	expectedInstance := omnistrate_api.ResourceInstance{
		InstanceID:    "test-instance",
		ResourceID:    "test-resource-id",
		ResourceAlias: "test-resource",
	}
	mockClient.On("RemoveCapacity", ctx, "test-resource", uint(2)).Return(expectedInstance, nil).Once()

	// Second iteration: waitForActiveState shows capacity is now 0 (target reached)
	finalCapacity := currentCapacity
	finalCapacity.CurrentCapacity = 0
	mockClient.On("GetCurrentCapacity", ctx, "test-resource").Return(finalCapacity, nil).Once()

	// Call ScaleToTarget to scale down to 0
	err := autoscaler.ScaleToTarget(ctx, 0)

	// Assertions
	assert.NoError(t, err)
	mockClient.AssertExpectations(t)
}

func TestWaitForActiveState_Success(t *testing.T) {
	mockClient := new(MockClient)
	autoscaler := createTestAutoscaler(t, mockClient)
	ctx := context.Background()

	// Mock instance with STARTING status first
	startingCapacity := omnistrate_api.ResourceInstanceCapacity{
		InstanceID:      "test-instance",
		Status:          omnistrate_api.STARTING,
		ResourceID:      "test-resource-id",
		ResourceAlias:   "test-resource",
		CurrentCapacity: 2,
	}

	activeCapacity := startingCapacity
	activeCapacity.Status = omnistrate_api.ACTIVE

	// First iteration: waitForActiveState polls and gets STARTING, then ACTIVE
	mockClient.On("GetCurrentCapacity", ctx, "test-resource").Return(startingCapacity, nil).Once()
	mockClient.On("GetCurrentCapacity", ctx, "test-resource").Return(activeCapacity, nil).Once()

	// Mock the AddCapacity call (scaling from 2 to 3)
	expectedInstance := omnistrate_api.ResourceInstance{
		InstanceID:    "test-instance",
		ResourceID:    "test-resource-id",
		ResourceAlias: "test-resource",
	}
	mockClient.On("AddCapacity", ctx, "test-resource", uint(1)).Return(expectedInstance, nil).Once()

	// Second iteration: waitForActiveState shows capacity of 3 (target reached, loop exits)
	finalCapacity := activeCapacity
	finalCapacity.CurrentCapacity = 3
	mockClient.On("GetCurrentCapacity", ctx, "test-resource").Return(finalCapacity, nil).Once()

	// Call ScaleToTarget to trigger waitForActiveState behavior
	err := autoscaler.ScaleToTarget(ctx, 3)

	// Assertions
	assert.NoError(t, err)
	mockClient.AssertExpectations(t)
}

func TestWaitForActiveState_Failed(t *testing.T) {
	mockClient := new(MockClient)
	autoscaler := createTestAutoscaler(t, mockClient)
	ctx := context.Background()

	// Mock instance with FAILED status
	failedCapacity := omnistrate_api.ResourceInstanceCapacity{
		InstanceID:      "test-instance",
		Status:          omnistrate_api.FAILED,
		ResourceID:      "test-resource-id",
		ResourceAlias:   "test-resource",
		CurrentCapacity: 3,
	}

	// Mock waitForActiveState polling that returns FAILED
	mockClient.On("GetCurrentCapacity", ctx, "test-resource").Return(failedCapacity, nil).Once()

	// Call ScaleToTarget which will trigger waitForActiveState
	err := autoscaler.ScaleToTarget(ctx, 2) // Different target to trigger scaling

	// Assertions
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "instance is in FAILED state")
	mockClient.AssertExpectations(t)
}

func TestScaleToTarget_ScaleDownBeyondMinimum(t *testing.T) {
	mockClient := new(MockClient)
	autoscaler := createTestAutoscaler(t, mockClient)
	ctx := context.Background()

	// First iteration: waitForActiveState returns capacity = 1, status = ACTIVE
	currentCapacity := omnistrate_api.ResourceInstanceCapacity{
		InstanceID:      "test-instance",
		Status:          omnistrate_api.ACTIVE,
		ResourceID:      "test-resource-id",
		ResourceAlias:   "test-resource",
		CurrentCapacity: 1,
	}
	mockClient.On("GetCurrentCapacity", ctx, "test-resource").Return(currentCapacity, nil).Once()

	// Mock the RemoveCapacity call - should only remove 1 (current capacity), not steps
	expectedInstance := omnistrate_api.ResourceInstance{
		InstanceID:    "test-instance",
		ResourceID:    "test-resource-id",
		ResourceAlias: "test-resource",
	}
	mockClient.On("RemoveCapacity", ctx, "test-resource", uint(1)).Return(expectedInstance, nil).Once()

	// Second iteration: waitForActiveState shows capacity is now 0 (target reached)
	finalCapacity := currentCapacity
	finalCapacity.CurrentCapacity = 0
	mockClient.On("GetCurrentCapacity", ctx, "test-resource").Return(finalCapacity, nil).Once()

	// Call ScaleToTarget to scale down to 0
	err := autoscaler.ScaleToTarget(ctx, 0)

	// Assertions
	assert.NoError(t, err)
	mockClient.AssertExpectations(t)
}

func TestScaleToTarget_ConcurrentRequestBlocked(t *testing.T) {
	mockClient := new(MockClient)
	autoscaler := createTestAutoscaler(t, mockClient)
	ctx := context.Background()

	// Mock first scaling operation
	currentCapacity := omnistrate_api.ResourceInstanceCapacity{
		InstanceID:      "test-instance",
		Status:          omnistrate_api.ACTIVE,
		ResourceID:      "test-resource-id",
		ResourceAlias:   "test-resource",
		CurrentCapacity: 1,
	}

	// First call to waitForActiveState in the first scaling operation
	mockClient.On("GetCurrentCapacity", ctx, "test-resource").Return(currentCapacity, nil).Once()

	// Mock AddCapacity call
	expectedInstance := omnistrate_api.ResourceInstance{
		InstanceID:    "test-instance",
		ResourceID:    "test-resource-id",
		ResourceAlias: "test-resource",
	}
	mockClient.On("AddCapacity", ctx, "test-resource", uint(1)).Return(expectedInstance, nil).Once()

	// Second call to waitForActiveState - simulate long operation by returning capacity not yet at target
	startingCapacity := currentCapacity
	startingCapacity.Status = omnistrate_api.STARTING
	startingCapacity.CurrentCapacity = 1
	mockClient.On("GetCurrentCapacity", ctx, "test-resource").Return(startingCapacity, nil).Times(100)

	// Eventually return target reached
	finalCapacity := currentCapacity
	finalCapacity.CurrentCapacity = 2
	finalCapacity.Status = omnistrate_api.ACTIVE
	mockClient.On("GetCurrentCapacity", ctx, "test-resource").Return(finalCapacity, nil).Maybe()

	// Start first scaling operation in goroutine
	errChan1 := make(chan error, 1)
	go func() {
		errChan1 <- autoscaler.ScaleToTarget(ctx, 2)
	}()

	// Give first operation time to start
	time.Sleep(100 * time.Millisecond)

	// Try to start second scaling operation - should fail immediately
	err := autoscaler.ScaleToTarget(ctx, 3)

	// Assertions
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "scaling operation already in progress")
	assert.Contains(t, err.Error(), "to target capacity: 2")

	// Wait for first operation to complete (or timeout)
	select {
	case <-errChan1:
		// First operation completed
	case <-time.After(2 * time.Second):
		// Timeout - that's okay for this test
	}
}

func TestGetStatus_DuringScaling(t *testing.T) {
	mockClient := new(MockClient)
	autoscaler := createTestAutoscaler(t, mockClient)
	ctx := context.Background()

	// Mock scaling operation in progress
	currentCapacity := omnistrate_api.ResourceInstanceCapacity{
		InstanceID:      "test-instance",
		Status:          omnistrate_api.STARTING,
		ResourceID:      "test-resource-id",
		ResourceAlias:   "test-resource",
		CurrentCapacity: 1,
	}

	// Start scaling operation in background
	mockClient.On("GetCurrentCapacity", ctx, "test-resource").Return(currentCapacity, nil).Maybe()
	mockClient.On("AddCapacity", ctx, "test-resource", uint(1)).Return(omnistrate_api.ResourceInstance{
		InstanceID:    "test-instance",
		ResourceID:    "test-resource-id",
		ResourceAlias: "test-resource",
	}, nil).Maybe()

	// Start scaling in goroutine
	go func() {
		err := autoscaler.ScaleToTarget(ctx, 3)
		assert.NoError(t, err)
	}()

	// Give it time to start
	time.Sleep(100 * time.Millisecond)

	// Check status while scaling
	status, err := autoscaler.GetStatus(ctx)

	// Assertions
	assert.NoError(t, err)
	assert.NotNil(t, status)
	assert.True(t, status.ScalingInProgress)
	assert.Equal(t, 3, status.TargetCapacity)
	// Verify resource metadata fields
	assert.Equal(t, "test-instance", status.InstanceID)
	assert.Equal(t, "test-resource-id", status.ResourceID)
	assert.Equal(t, "test-resource", status.ResourceAlias)
}

func TestGetStatus_WithCooldown(t *testing.T) {
	mockClient := new(MockClient)
	// Create autoscaler with 5 second cooldown
	t.Setenv("AUTOSCALER_COOLDOWN", "5")
	t.Setenv("AUTOSCALER_TARGET_RESOURCES", "test-resource")
	t.Setenv("AUTOSCALER_STEPS", "1")
	t.Setenv("DRY_RUN", "true")
	t.Setenv("AUTOSCALER_WAIT_FOR_ACTIVE_TIMEOUT", "10")
	t.Setenv("AUTOSCALER_WAIT_FOR_ACTIVE_CHECK_INTERVAL", "1")
	config, _ := config.NewConfigFromEnv()
	autoscaler := &Autoscaler{
		config:         config,
		client:         mockClient,
		lastActionTime: time.Now().Add(-3 * time.Second), // 3 seconds ago
	}
	ctx := context.Background()

	// Mock GetCurrentCapacity
	currentCapacity := omnistrate_api.ResourceInstanceCapacity{
		InstanceID:      "test-instance",
		Status:          omnistrate_api.ACTIVE,
		ResourceID:      "test-resource-id",
		ResourceAlias:   "test-resource",
		CurrentCapacity: 2,
	}
	mockClient.On("GetCurrentCapacity", ctx, "test-resource").Return(currentCapacity, nil)

	// Check status
	status, err := autoscaler.GetStatus(ctx)

	// Assertions
	assert.NoError(t, err)
	assert.NotNil(t, status)
	assert.True(t, status.InCooldownPeriod)
	assert.Greater(t, status.CooldownRemaining, time.Duration(0))
	assert.Less(t, status.CooldownRemaining, 5*time.Second)
	mockClient.AssertExpectations(t)
}

func TestScaleUp_LimitedByTargetCapacity(t *testing.T) {
	mockClient := new(MockClient)
	autoscaler := createTestAutoscaler(t, mockClient)
	autoscaler.config.Steps = 5 // Set steps to 5, but target capacity only requires 2
	ctx := context.Background()

	// First iteration: waitForActiveState returns capacity = 1, status = ACTIVE
	currentCapacity := omnistrate_api.ResourceInstanceCapacity{
		InstanceID:      "test-instance",
		Status:          omnistrate_api.ACTIVE,
		ResourceID:      "test-resource-id",
		ResourceAlias:   "test-resource",
		CurrentCapacity: 1,
	}
	mockClient.On("GetCurrentCapacity", ctx, "test-resource").Return(currentCapacity, nil).Once()

	// Mock the AddCapacity call - should only add 2 (target - current), not 5 (steps)
	expectedInstance := omnistrate_api.ResourceInstance{
		InstanceID:    "test-instance",
		ResourceID:    "test-resource-id",
		ResourceAlias: "test-resource",
	}
	mockClient.On("AddCapacity", ctx, "test-resource", uint(2)).Return(expectedInstance, nil).Once()

	// Second iteration: waitForActiveState shows capacity is now 3 (target reached, loop exits)
	finalCapacity := currentCapacity
	finalCapacity.CurrentCapacity = 3
	mockClient.On("GetCurrentCapacity", ctx, "test-resource").Return(finalCapacity, nil).Once()

	// Call ScaleToTarget (need to scale up from 1 to 3, which is 2 steps, not 5)
	err := autoscaler.ScaleToTarget(ctx, 3)

	// Assertions
	assert.NoError(t, err)
	mockClient.AssertExpectations(t)
}

func TestScaleDown_LimitedByTargetCapacity(t *testing.T) {
	mockClient := new(MockClient)
	autoscaler := createTestAutoscaler(t, mockClient)
	autoscaler.config.Steps = 5 // Set steps to 5, but target capacity only requires removing 2
	ctx := context.Background()

	// First iteration: waitForActiveState returns capacity = 5, status = ACTIVE
	currentCapacity := omnistrate_api.ResourceInstanceCapacity{
		InstanceID:      "test-instance",
		Status:          omnistrate_api.ACTIVE,
		ResourceID:      "test-resource-id",
		ResourceAlias:   "test-resource",
		CurrentCapacity: 5,
	}
	mockClient.On("GetCurrentCapacity", ctx, "test-resource").Return(currentCapacity, nil).Once()

	// Mock the RemoveCapacity call - should only remove 2 (current - target), not 5 (steps)
	expectedInstance := omnistrate_api.ResourceInstance{
		InstanceID:    "test-instance",
		ResourceID:    "test-resource-id",
		ResourceAlias: "test-resource",
	}
	mockClient.On("RemoveCapacity", ctx, "test-resource", uint(2)).Return(expectedInstance, nil).Once()

	// Second iteration: waitForActiveState shows capacity is now 3 (target reached, loop exits)
	finalCapacity := currentCapacity
	finalCapacity.CurrentCapacity = 3
	mockClient.On("GetCurrentCapacity", ctx, "test-resource").Return(finalCapacity, nil).Once()

	// Call ScaleToTarget (need to scale down from 5 to 3, which is 2 steps, not 5)
	err := autoscaler.ScaleToTarget(ctx, 3)

	// Assertions
	assert.NoError(t, err)
	mockClient.AssertExpectations(t)
}
