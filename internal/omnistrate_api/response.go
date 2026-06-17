package omnistrate_api

import (
	"github.com/go-openapi/strfmt"
)

type ResourceInstanceCapacity struct {
	InstanceID            string          `json:"instanceId"`
	Status                Status          `json:"status"`
	ResourceID            string          `json:"resourceId"`
	ResourceAlias         string          `json:"resourceAlias"`
	CurrentCapacity       int             `json:"currentCapacity"`
	LastObservedTimestamp strfmt.DateTime `json:"lastObservedTimestamp"`
}

type ResourceInstance struct {
	InstanceID    string `json:"instanceId,omitempty"`
	ResourceID    string `json:"resourceId,omitempty"`
	ResourceAlias string `json:"resourceAlias,omitempty"`
}

type ResourceCapacityChange struct {
	ResourceAlias       string `json:"resourceAlias"`
	CapacityToBeAdded   uint   `json:"capacityToBeAdded,omitempty"`
	CapacityToBeRemoved uint   `json:"capacityToBeRemoved,omitempty"`
}

type ResourceInstances struct {
	InstanceID string             `json:"instanceId,omitempty"`
	Resources  []ResourceInstance `json:"resources,omitempty"`
}

type Status string

const (
	ACTIVE   Status = "ACTIVE"
	STARTING Status = "STARTING"
	PAUSED   Status = "PAUSED"
	FAILED   Status = "FAILED"
	UNKNOWN  Status = "UNKNOWN"
)
