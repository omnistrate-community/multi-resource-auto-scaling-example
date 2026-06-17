package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	CooldownDuration           time.Duration
	TargetResource             string
	TargetResources            []string
	Steps                      uint
	DryRun                     bool
	WaitForActiveTimeout       time.Duration
	WaitForActiveCheckInterval time.Duration
}

// NewConfigFromEnv loads configuration from environment variables
func NewConfigFromEnv() (*Config, error) {
	// Get cooldown duration
	cooldownStr := os.Getenv("AUTOSCALER_COOLDOWN")
	if cooldownStr == "" {
		cooldownStr = "300" // Default 5 minutes
	}
	cooldownSeconds, err := strconv.Atoi(cooldownStr)
	if err != nil {
		return nil, fmt.Errorf("invalid AUTOSCALER_COOLDOWN value: %s", cooldownStr)
	}

	// Get target resources. AUTOSCALER_TARGET_RESOURCE remains supported for
	// existing single-resource deployments.
	targetResources, err := parseTargetResources(os.Getenv("AUTOSCALER_TARGET_RESOURCES"))
	if err != nil {
		return nil, err
	}
	if len(targetResources) == 0 {
		targetResource := strings.TrimSpace(os.Getenv("AUTOSCALER_TARGET_RESOURCE"))
		if targetResource == "" {
			return nil, fmt.Errorf("AUTOSCALER_TARGET_RESOURCES or AUTOSCALER_TARGET_RESOURCE environment variable is required")
		}
		targetResources = []string{targetResource}
	}

	// Get steps
	stepsStr := os.Getenv("AUTOSCALER_STEPS")
	if stepsStr == "" {
		stepsStr = "1" // Default 1 step
	}
	steps, err := strconv.Atoi(stepsStr)
	if err != nil {
		return nil, fmt.Errorf("invalid AUTOSCALER_STEPS value: %s", stepsStr)
	}

	// Get dry run flag
	dryRunStr := os.Getenv("DRY_RUN")
	dryRun := false // Default to false
	if dryRunStr != "" {
		dryRun, err = strconv.ParseBool(dryRunStr)
		if err != nil {
			return nil, fmt.Errorf("invalid DRY_RUN value: %s", dryRunStr)
		}
	}

	// Get wait for active timeout
	waitForActiveTimeoutStr := os.Getenv("AUTOSCALER_WAIT_FOR_ACTIVE_TIMEOUT")
	if waitForActiveTimeoutStr == "" {
		waitForActiveTimeoutStr = "900" // Default 15 minutes
	}
	waitForActiveTimeoutSeconds, err := strconv.Atoi(waitForActiveTimeoutStr)
	if err != nil {
		return nil, fmt.Errorf("invalid AUTOSCALER_WAIT_FOR_ACTIVE_TIMEOUT value: %s", waitForActiveTimeoutStr)
	}

	// Get wait for active check interval
	waitForActiveCheckIntervalStr := os.Getenv("AUTOSCALER_WAIT_FOR_ACTIVE_CHECK_INTERVAL")
	if waitForActiveCheckIntervalStr == "" {
		waitForActiveCheckIntervalStr = "30" // Default 30 seconds
	}
	waitForActiveCheckIntervalSeconds, err := strconv.Atoi(waitForActiveCheckIntervalStr)
	if err != nil {
		return nil, fmt.Errorf("invalid AUTOSCALER_WAIT_FOR_ACTIVE_CHECK_INTERVAL value: %s", waitForActiveCheckIntervalStr)
	}

	return &Config{
		CooldownDuration:           time.Duration(cooldownSeconds) * time.Second,
		TargetResource:             targetResources[0],
		TargetResources:            targetResources,
		Steps:                      uint(steps),
		DryRun:                     dryRun,
		WaitForActiveTimeout:       time.Duration(waitForActiveTimeoutSeconds) * time.Second,
		WaitForActiveCheckInterval: time.Duration(waitForActiveCheckIntervalSeconds) * time.Second,
	}, nil
}

func parseTargetResources(value string) ([]string, error) {
	if strings.TrimSpace(value) == "" {
		return nil, nil
	}

	parts := strings.Split(value, ",")
	resources := make([]string, 0, len(parts))
	for _, part := range parts {
		resource := strings.TrimSpace(part)
		if resource == "" {
			return nil, fmt.Errorf("AUTOSCALER_TARGET_RESOURCES contains an empty resource alias")
		}
		resources = append(resources, resource)
	}
	return resources, nil
}
