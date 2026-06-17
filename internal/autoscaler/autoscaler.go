package autoscaler

import (
	"context"
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/omnistrate-community/multi-resource-auto-scaling-example/internal/config"
	"github.com/omnistrate-community/multi-resource-auto-scaling-example/internal/logger"
	"github.com/omnistrate-community/multi-resource-auto-scaling-example/internal/omnistrate_api"
)

type Autoscaler struct {
	config            *config.Config
	client            omnistrate_api.Client
	lastActionTime    time.Time
	scalingInProgress bool
	targetCapacity    int
	mu                sync.RWMutex
}

// ScalingStatus represents the current status of the autoscaler
type ScalingStatus struct {
	CurrentCapacity   int
	TargetCapacity    int
	Status            omnistrate_api.Status
	ScalingInProgress bool
	LastActionTime    time.Time
	InCooldownPeriod  bool
	CooldownRemaining time.Duration
	InstanceID        string
	ResourceID        string
	ResourceAlias     string
}

type MultiResourceScalingStatus struct {
	Resources         map[string]ScalingStatus
	ScalingInProgress bool
	LastActionTime    time.Time
	InCooldownPeriod  bool
	CooldownRemaining time.Duration
}

// NewAutoscaler creates a new autoscaler instance with configuration from environment variables
func NewAutoscaler(ctx context.Context) (*Autoscaler, error) {
	config, err := config.NewConfigFromEnv()
	if err != nil {
		return nil, fmt.Errorf("failed to load configuration: %w", err)
	}

	client := omnistrate_api.NewClient(config)

	return &Autoscaler{
		config: config,
		client: client,
	}, nil
}

// ScaleToTarget scales the resource to match the target capacity
func (a *Autoscaler) ScaleToTarget(ctx context.Context, targetCapacity int) error {
	// Check if scaling is already in progress
	a.mu.Lock()
	if a.scalingInProgress {
		a.mu.Unlock()
		return fmt.Errorf("scaling operation already in progress to target capacity: %d", a.targetCapacity)
	}
	a.scalingInProgress = true
	a.targetCapacity = targetCapacity
	a.mu.Unlock()

	// Ensure we mark scaling as complete when done
	defer func() {
		a.mu.Lock()
		a.scalingInProgress = false
		a.targetCapacity = 0
		a.mu.Unlock()
	}()

	logger.Info().Int("targetCapacity", targetCapacity).Msg("Scaling to target capacity")

	for {
		// Check if we're within cooldown period
		a.mu.RLock()
		lastAction := a.lastActionTime
		a.mu.RUnlock()

		if !lastAction.IsZero() && time.Since(lastAction) < a.config.CooldownDuration {
			waitTime := a.config.CooldownDuration - time.Since(lastAction)
			logger.Info().Dur("waitTime", waitTime).Msg("Within cooldown period, waiting before scaling")
			time.Sleep(waitTime)
		}

		// Wait for instance to be in ACTIVE state
		currentCapacity, err := a.waitForActiveState(ctx)
		if err != nil {
			return fmt.Errorf("failed to wait for active state: %w", err)
		}
		logger.Info().
			Int("currentCapacity", currentCapacity.CurrentCapacity).
			Int("targetCapacity", targetCapacity).
			Msg("Current and target capacity")

		// Check again if scaling is needed
		if currentCapacity.CurrentCapacity == targetCapacity {
			logger.Info().Int("capacity", targetCapacity).Msg("Reached target capacity")
			break
		}

		// Perform scaling operation
		if currentCapacity.CurrentCapacity < targetCapacity {
			err = a.scaleUp(ctx, currentCapacity.CurrentCapacity)
		} else {
			err = a.scaleDown(ctx, currentCapacity.CurrentCapacity)
		}

		if err != nil {
			return fmt.Errorf("failed to scale: %w", err)
		}

		// Update last action time
		a.mu.Lock()
		a.lastActionTime = time.Now()
		a.mu.Unlock()
	}

	return nil
}

// ScaleTargets scales multiple configured resources in one grouped sidecar request
// per direction. Existing single-resource callers should keep using ScaleToTarget.
func (a *Autoscaler) ScaleTargets(ctx context.Context, targets map[string]int) error {
	if len(targets) == 0 {
		return fmt.Errorf("no target capacities provided")
	}
	for resource, target := range targets {
		if target < 0 {
			return fmt.Errorf("target capacity for resource %s must be non-negative", resource)
		}
		if !a.isConfiguredResource(resource) {
			return fmt.Errorf("resource %s is not configured for autoscaling", resource)
		}
	}

	a.mu.Lock()
	if a.scalingInProgress {
		a.mu.Unlock()
		return fmt.Errorf("scaling operation already in progress")
	}
	a.scalingInProgress = true
	a.mu.Unlock()

	defer func() {
		a.mu.Lock()
		a.scalingInProgress = false
		a.mu.Unlock()
	}()

	a.mu.RLock()
	lastAction := a.lastActionTime
	a.mu.RUnlock()
	if !lastAction.IsZero() && time.Since(lastAction) < a.config.CooldownDuration {
		waitTime := a.config.CooldownDuration - time.Since(lastAction)
		logger.Info().Dur("waitTime", waitTime).Msg("Within cooldown period, waiting before grouped scaling")
		time.Sleep(waitTime)
	}

	capacities, err := a.getActiveCapacities(ctx, targets)
	if err != nil {
		return err
	}

	adds := make([]omnistrate_api.ResourceCapacityChange, 0, len(targets))
	removes := make([]omnistrate_api.ResourceCapacityChange, 0, len(targets))
	for resource, target := range targets {
		current := capacities[resource].CurrentCapacity
		switch {
		case current < target:
			steps := uint(math.Min(float64(a.config.Steps), float64(target-current)))
			if steps > 0 {
				adds = append(adds, omnistrate_api.ResourceCapacityChange{
					ResourceAlias:     resource,
					CapacityToBeAdded: steps,
				})
			}
		case current > target:
			steps := uint(math.Min(float64(a.config.Steps), float64(current-target)))
			if steps > 0 {
				removes = append(removes, omnistrate_api.ResourceCapacityChange{
					ResourceAlias:       resource,
					CapacityToBeRemoved: steps,
				})
			}
		}
	}

	if len(adds) == 0 && len(removes) == 0 {
		logger.Info().Msg("All resources already match target capacity")
		return nil
	}

	if len(adds) > 0 {
		if _, err := a.client.AddCapacities(ctx, adds); err != nil {
			return fmt.Errorf("failed to add grouped capacity: %w", err)
		}
	}
	if len(removes) > 0 {
		if _, err := a.client.RemoveCapacities(ctx, removes); err != nil {
			return fmt.Errorf("failed to remove grouped capacity: %w", err)
		}
	}

	a.mu.Lock()
	a.lastActionTime = time.Now()
	a.mu.Unlock()

	return nil
}

// getCurrentCapacity gets the current capacity of the resource
func (a *Autoscaler) getCurrentCapacity(ctx context.Context) (*omnistrate_api.ResourceInstanceCapacity, error) {
	capacity, err := a.client.GetCurrentCapacity(ctx, a.config.TargetResource)
	if err != nil {
		return nil, err
	}
	return &capacity, nil
}

func (a *Autoscaler) getActiveCapacities(ctx context.Context, targets map[string]int) (map[string]omnistrate_api.ResourceInstanceCapacity, error) {
	capacities := make(map[string]omnistrate_api.ResourceInstanceCapacity, len(targets))
	for resource := range targets {
		capacity, err := a.client.GetCurrentCapacity(ctx, resource)
		if err != nil {
			return nil, fmt.Errorf("failed to get current capacity for resource %s: %w", resource, err)
		}
		if capacity.Status != omnistrate_api.ACTIVE {
			return nil, fmt.Errorf("resource %s is not ACTIVE: %s", resource, capacity.Status)
		}
		capacities[resource] = capacity
	}
	return capacities, nil
}

// waitForActiveState waits for the instance to be in ACTIVE state
func (a *Autoscaler) waitForActiveState(ctx context.Context) (*omnistrate_api.ResourceInstanceCapacity, error) {
	maxWaitTime := a.config.WaitForActiveTimeout
	checkInterval := a.config.WaitForActiveCheckInterval
	timeout := time.After(maxWaitTime)
	ticker := time.NewTicker(checkInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-timeout:
			return nil, fmt.Errorf("timeout waiting for instance to become ACTIVE")
		case <-ticker.C:
			capacity, err := a.getCurrentCapacity(ctx)
			if err != nil {
				logger.Warn().Err(err).Msg("Error checking instance status")
				continue
			}

			logger.Debug().Str("status", string(capacity.Status)).Msg("Current instance status")
			if capacity.Status == omnistrate_api.ACTIVE {
				logger.Info().Msg("Instance is now ACTIVE")
				return capacity, nil
			}

			if capacity.Status == omnistrate_api.FAILED {
				return nil, fmt.Errorf("instance is in FAILED state")
			}

			logger.Debug().Str("status", string(capacity.Status)).Msg("Instance status is not ACTIVE, waiting")
		}
	}
}

// scaleUp adds capacity to the resource
func (a *Autoscaler) scaleUp(ctx context.Context, currentCapacity int) error {
	// Ensure we do not exceed target capacity
	steps := uint(math.Min(float64(a.config.Steps), float64(a.targetCapacity-currentCapacity)))
	if steps <= 0 {
		logger.Info().Msg("No scaling up needed")
		return nil
	}
	logger.Info().
		Int("currentCapacity", currentCapacity).
		Uint("increaseBy", steps).
		Msg("Scaling up instances")
	_, err := a.client.AddCapacity(ctx, a.config.TargetResource, steps)
	if err != nil {
		return fmt.Errorf("failed to add capacity: %w", err)
	}
	logger.Info().Uint("increaseBy", steps).Msg("Requested to add capacity")

	return nil
}

// scaleDown removes capacity from the resource
func (a *Autoscaler) scaleDown(ctx context.Context, currentCapacity int) error {
	steps := uint(math.Min(float64(a.config.Steps), float64(currentCapacity-a.targetCapacity)))
	if steps <= 0 {
		logger.Info().Msg("No scaling down needed")
		return nil
	}
	// Ensure we do not remove more capacity than currently exists
	logger.Info().
		Int("currentCapacity", currentCapacity).
		Uint("decreaseBy", steps).
		Msg("Scaling down instances")
	_, err := a.client.RemoveCapacity(ctx, a.config.TargetResource, steps)
	if err != nil {
		return fmt.Errorf("failed to remove capacity: %w", err)
	}
	logger.Info().Uint("decreaseBy", steps).Msg("Requested to remove capacity")
	return nil
}

// GetStatus returns the current status of the resource including scaling state
func (a *Autoscaler) GetStatus(ctx context.Context) (*ScalingStatus, error) {
	capacity, err := a.getCurrentCapacity(ctx)
	if err != nil {
		return nil, err
	}

	a.mu.RLock()
	defer a.mu.RUnlock()

	status := &ScalingStatus{
		CurrentCapacity:   capacity.CurrentCapacity,
		TargetCapacity:    a.targetCapacity,
		ScalingInProgress: a.scalingInProgress,
		LastActionTime:    a.lastActionTime,
		Status:            capacity.Status,
		InstanceID:        capacity.InstanceID,
		ResourceID:        capacity.ResourceID,
		ResourceAlias:     capacity.ResourceAlias,
	}

	// Calculate cooldown information
	if !a.lastActionTime.IsZero() {
		timeSinceLastAction := time.Since(a.lastActionTime)
		if timeSinceLastAction < a.config.CooldownDuration {
			status.InCooldownPeriod = true
			status.CooldownRemaining = a.config.CooldownDuration - timeSinceLastAction
		}
	}

	return status, nil
}

func (a *Autoscaler) GetStatuses(ctx context.Context) (*MultiResourceScalingStatus, error) {
	resources := a.config.TargetResources
	if len(resources) == 0 {
		resources = []string{a.config.TargetResource}
	}

	statuses := make(map[string]ScalingStatus, len(resources))
	for _, resource := range resources {
		capacity, err := a.client.GetCurrentCapacity(ctx, resource)
		if err != nil {
			return nil, err
		}
		statuses[resource] = ScalingStatus{
			CurrentCapacity: capacity.CurrentCapacity,
			Status:          capacity.Status,
			InstanceID:      capacity.InstanceID,
			ResourceID:      capacity.ResourceID,
			ResourceAlias:   capacity.ResourceAlias,
		}
	}

	a.mu.RLock()
	defer a.mu.RUnlock()

	status := &MultiResourceScalingStatus{
		Resources:         statuses,
		ScalingInProgress: a.scalingInProgress,
		LastActionTime:    a.lastActionTime,
	}

	if !a.lastActionTime.IsZero() {
		timeSinceLastAction := time.Since(a.lastActionTime)
		if timeSinceLastAction < a.config.CooldownDuration {
			status.InCooldownPeriod = true
			status.CooldownRemaining = a.config.CooldownDuration - timeSinceLastAction
		}
	}

	return status, nil
}

func (a *Autoscaler) isConfiguredResource(resource string) bool {
	resources := a.config.TargetResources
	if len(resources) == 0 {
		resources = []string{a.config.TargetResource}
	}
	for _, configured := range resources {
		if configured == resource {
			return true
		}
	}
	return false
}

// GetConfig returns the current configuration
func (a *Autoscaler) GetConfig() *config.Config {
	return a.config
}
