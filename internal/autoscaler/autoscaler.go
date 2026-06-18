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
	targetCapacities  map[string]int
	mu                sync.RWMutex
}

// ScalingStatus represents the current status of the autoscaler
type ScalingStatus struct {
	CurrentCapacity   int                   `json:"currentCapacity"`
	TargetCapacity    int                   `json:"targetCapacity"`
	Status            omnistrate_api.Status `json:"status"`
	ScalingInProgress bool                  `json:"scalingInProgress"`
	LastActionTime    time.Time             `json:"lastActionTime"`
	InCooldownPeriod  bool                  `json:"inCooldownPeriod"`
	CooldownRemaining time.Duration         `json:"cooldownRemaining"`
	InstanceID        string                `json:"instanceId"`
	ResourceID        string                `json:"resourceId"`
	ResourceAlias     string                `json:"resourceAlias"`
}

type MultiResourceScalingStatus struct {
	Resources         map[string]ScalingStatus `json:"resources"`
	ScalingInProgress bool                     `json:"scalingInProgress"`
	LastActionTime    time.Time                `json:"lastActionTime"`
	InCooldownPeriod  bool                     `json:"inCooldownPeriod"`
	CooldownRemaining time.Duration            `json:"cooldownRemaining"`
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

func NewAutoscalerWithClient(config *config.Config, client omnistrate_api.Client) *Autoscaler {
	return &Autoscaler{
		config: config,
		client: client,
	}
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
	a.targetCapacities = copyTargets(targets)
	a.mu.Unlock()

	defer func() {
		a.mu.Lock()
		a.scalingInProgress = false
		a.targetCapacities = nil
		a.mu.Unlock()
	}()

	for {
		a.mu.RLock()
		lastAction := a.lastActionTime
		a.mu.RUnlock()
		if !lastAction.IsZero() && time.Since(lastAction) < a.config.CooldownDuration {
			waitTime := a.config.CooldownDuration - time.Since(lastAction)
			logger.Info().Dur("waitTime", waitTime).Msg("Within cooldown period, waiting before grouped scaling")
			time.Sleep(waitTime)
		}

		capacities, err := a.waitForActiveCapacities(ctx, targets)
		if err != nil {
			return err
		}

		adds := make([]omnistrate_api.ResourceCapacityChange, 0, len(targets))
		removes := make([]omnistrate_api.ResourceCapacityChange, 0, len(targets))
		for _, resource := range a.targetResourceOrder(targets) {
			target := targets[resource]
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
			logger.Info().Msg("All resources reached target capacity")
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
	}
}

func copyTargets(targets map[string]int) map[string]int {
	copied := make(map[string]int, len(targets))
	for resource, target := range targets {
		copied[resource] = target
	}
	return copied
}

func (a *Autoscaler) targetResourceOrder(targets map[string]int) []string {
	ordered := make([]string, 0, len(targets))
	for _, resource := range a.config.TargetResources {
		if _, ok := targets[resource]; ok {
			ordered = append(ordered, resource)
		}
	}
	if len(ordered) == len(targets) {
		return ordered
	}
	for resource := range targets {
		if !containsResource(ordered, resource) {
			ordered = append(ordered, resource)
		}
	}
	return ordered
}

// getCurrentCapacity gets the current capacity of the resource
func (a *Autoscaler) getCurrentCapacity(ctx context.Context) (*omnistrate_api.ResourceInstanceCapacity, error) {
	capacity, err := a.client.GetCurrentCapacity(ctx, a.config.TargetResource)
	if err != nil {
		return nil, err
	}
	return &capacity, nil
}

func (a *Autoscaler) getCurrentCapacities(ctx context.Context, targets map[string]int) (map[string]omnistrate_api.ResourceInstanceCapacity, error) {
	capacities := make(map[string]omnistrate_api.ResourceInstanceCapacity, len(targets))
	for resource := range targets {
		capacity, err := a.client.GetCurrentCapacity(ctx, resource)
		if err != nil {
			return nil, fmt.Errorf("failed to get current capacity for resource %s: %w", resource, err)
		}
		capacities[resource] = capacity
	}
	return capacities, nil
}

func (a *Autoscaler) waitForActiveCapacities(ctx context.Context, targets map[string]int) (map[string]omnistrate_api.ResourceInstanceCapacity, error) {
	maxWaitTime := a.config.WaitForActiveTimeout
	checkInterval := a.config.WaitForActiveCheckInterval
	timeout := time.After(maxWaitTime)
	ticker := time.NewTicker(checkInterval)
	defer ticker.Stop()

	for {
		capacities, err := a.getCurrentCapacities(ctx, targets)
		if err != nil {
			logger.Warn().Err(err).Msg("Error checking resource statuses")
		} else {
			allActive := true
			for resource, capacity := range capacities {
				if capacity.Status == omnistrate_api.FAILED {
					return nil, fmt.Errorf("resource %s is in FAILED state", resource)
				}
				if capacity.Status != omnistrate_api.ACTIVE {
					allActive = false
				}
			}
			if allActive {
				return capacities, nil
			}
		}

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-timeout:
			return nil, fmt.Errorf("timeout waiting for resources to become ACTIVE")
		case <-ticker.C:
		}
	}
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
	targetCapacities := copyTargets(a.targetCapacities)
	scalingInProgress := a.scalingInProgress
	lastActionTime := a.lastActionTime
	defer a.mu.RUnlock()

	status := &MultiResourceScalingStatus{
		Resources:         statuses,
		ScalingInProgress: scalingInProgress,
		LastActionTime:    lastActionTime,
	}
	for resource, resourceStatus := range status.Resources {
		resourceStatus.TargetCapacity = targetCapacities[resource]
		resourceStatus.ScalingInProgress = scalingInProgress
		resourceStatus.LastActionTime = lastActionTime
		status.Resources[resource] = resourceStatus
	}

	if !lastActionTime.IsZero() {
		timeSinceLastAction := time.Since(lastActionTime)
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

func containsResource(resources []string, resource string) bool {
	for _, candidate := range resources {
		if candidate == resource {
			return true
		}
	}
	return false
}

// GetConfig returns the current configuration
func (a *Autoscaler) GetConfig() *config.Config {
	return a.config
}
