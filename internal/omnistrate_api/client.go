package omnistrate_api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/go-openapi/strfmt"
	"github.com/hashicorp/go-retryablehttp"
	"github.com/omnistrate-community/multi-resource-auto-scaling-example/internal/config"
	"github.com/pkg/errors"
)

const (
	defaultBaseURL           = "http://127.0.0.1:49750"
	resourcePath             = "/resource/"
	addCapacityPath          = resourcePath + "%s/capacity/add"
	removeCapacityPath       = resourcePath + "%s/capacity/remove"
	getCapacityPath          = resourcePath + "%s/capacity"
	addCapacitiesPath        = "/resources/capacity/add"
	removeCapacitiesPath     = "/resources/capacity/remove"
	capacityToBeAddedField   = "capacityToBeAdded"
	capacityToBeRemovedField = "capacityToBeRemoved"
)

type Client interface {
	GetCurrentCapacity(ctx context.Context, resourceAlias string) (ResourceInstanceCapacity, error)
	AddCapacity(ctx context.Context, resourceAlias string, capacityToBeAdded uint) (ResourceInstance, error)
	RemoveCapacity(ctx context.Context, resourceAlias string, capacityToBeRemoved uint) (ResourceInstance, error)
	AddCapacities(ctx context.Context, changes []ResourceCapacityChange) (ResourceInstances, error)
	RemoveCapacities(ctx context.Context, changes []ResourceCapacityChange) (ResourceInstances, error)
}

/**
 * This file contains all APIs used to interact with omnistrate platform via local sidecar.
 */
type ClientImpl struct {
	config     *config.Config
	httpClient *retryablehttp.Client
	baseURL    string
}

func NewWithHTTPClient(config *config.Config, httpClient *retryablehttp.Client, baseURLs ...string) Client {
	baseURL := defaultBaseURL
	if len(baseURLs) > 0 && baseURLs[0] != "" {
		baseURL = baseURLs[0]
	}
	return &ClientImpl{config: config, httpClient: httpClient, baseURL: baseURL}
}

func NewClient(config *config.Config) Client {
	retryClient := retryablehttp.NewClient()
	retryClient.RetryMax = 3
	retryClient.RetryWaitMin = 1 * time.Second
	retryClient.RetryWaitMax = 30 * time.Second
	retryClient.HTTPClient.Timeout = 60 * time.Second
	return NewWithHTTPClient(config, retryClient)
}

func (c *ClientImpl) GetCurrentCapacity(ctx context.Context, resourceAlias string) (resp ResourceInstanceCapacity, err error) {
	if c.config.DryRun {
		return ResourceInstanceCapacity{
			InstanceID:            "instance-abc",
			ResourceID:            "resource-abc",
			ResourceAlias:         resourceAlias,
			Status:                ACTIVE,
			CurrentCapacity:       10,
			LastObservedTimestamp: strfmt.DateTime(time.Now().UTC()),
		}, nil
	}

	req, err := retryablehttp.NewRequestWithContext(ctx, http.MethodGet, c.urlFor(fmt.Sprintf(getCapacityPath, resourceAlias)), nil)
	if err != nil {
		return
	}
	httpResp, err := c.httpClient.Do(req)
	if err != nil {
		err = errors.Wrapf(err, "Failed get current capacity for resourceAlias: %s", resourceAlias)
		return
	}
	if httpResp.StatusCode != http.StatusOK {
		err = errors.Errorf("Failed get current capacity for resourceAlias: %s, status code: %d", resourceAlias, httpResp.StatusCode)
		return
	}
	defer func() {
		if closeErr := httpResp.Body.Close(); closeErr != nil {
			err = errors.Wrapf(closeErr, "Failed to close response body")
		}
	}()
	body, err := io.ReadAll(httpResp.Body)
	if err != nil {
		err = errors.Wrapf(err, "Failed read response body when querying current capacity for resourceAlias: %s", resourceAlias)
		return
	}
	err = json.Unmarshal(body, &resp)
	if err != nil {
		err = errors.Wrapf(err, "Failed unmarshal response body when querying current capacity for resourceAlias: %s", resourceAlias)
		return
	}
	return
}

func (c *ClientImpl) AddCapacity(ctx context.Context, resourceAlias string, capacityToBeAdded uint) (resp ResourceInstance, err error) {
	if c.config.DryRun {
		return ResourceInstance{
			InstanceID:    "instance-abc",
			ResourceID:    "resource-abc",
			ResourceAlias: resourceAlias,
		}, nil
	}

	if capacityToBeAdded == 0 {
		return ResourceInstance{
			InstanceID:    "instance-abc",
			ResourceID:    "resource-abc",
			ResourceAlias: resourceAlias,
		}, nil
	}

	reqBody := map[string]interface{}{
		capacityToBeAddedField: float64(capacityToBeAdded),
	}
	reqBytes, err := json.Marshal(reqBody)
	if err != nil {
		err = errors.Wrapf(err, "Failed marshal request body when adding capacity for resourceAlias: %s", resourceAlias)
		return
	}
	req, err := retryablehttp.NewRequestWithContext(ctx, http.MethodPost, c.urlFor(fmt.Sprintf(addCapacityPath, resourceAlias)), reqBytes)
	if err != nil {
		return
	}
	req.Header.Add("Content-Type", "application/json")
	httpResp, err := c.httpClient.Do(req)
	if err != nil {
		err = errors.Wrapf(err, "Failed to add capacity for resourceAlias: %s", resourceAlias)
		return
	}
	if httpResp.StatusCode != http.StatusOK {
		err = errors.Errorf("Failed to add capacity for resourceAlias: %s, status code: %d", resourceAlias, httpResp.StatusCode)
		return
	}
	defer func() {
		if closeErr := httpResp.Body.Close(); closeErr != nil {
			err = errors.Wrapf(closeErr, "Failed to close response body")
		}
	}()
	body, err := io.ReadAll(httpResp.Body)
	if err != nil {
		err = errors.Wrapf(err, "Failed read response body when adding capacity for resourceAlias: %s", resourceAlias)
		return
	}
	err = json.Unmarshal(body, &resp)
	if err != nil {
		err = errors.Wrapf(err, "Failed unmarshal response body when adding capacity for resourceAlias: %s", resourceAlias)
		return
	}
	return
}

func (c *ClientImpl) RemoveCapacity(ctx context.Context, resourceAlias string, capacityToBeRemoved uint) (resp ResourceInstance, err error) {
	if c.config.DryRun {
		return ResourceInstance{
			ResourceAlias: resourceAlias,
		}, nil
	}

	if capacityToBeRemoved == 0 {
		return ResourceInstance{
			ResourceAlias: resourceAlias,
		}, nil
	}

	reqBody := map[string]interface{}{
		capacityToBeRemovedField: float64(capacityToBeRemoved),
	}
	reqBytes, err := json.Marshal(reqBody)
	if err != nil {
		err = errors.Wrapf(err, "Failed marshal request body when removing capacity for resourceAlias: %s", resourceAlias)
		return
	}
	req, err := retryablehttp.NewRequestWithContext(ctx, http.MethodPost, c.urlFor(fmt.Sprintf(removeCapacityPath, resourceAlias)), reqBytes)
	if err != nil {
		err = errors.Wrapf(err, "Failed to create remove capacity request for resourceAlias: %s", resourceAlias)
		return
	}
	req.Header.Add("Content-Type", "application/json")
	httpResp, err := c.httpClient.Do(req)
	if err != nil {
		err = errors.Wrapf(err, "Failed to remove capacity for resourceAlias: %s", resourceAlias)
		return
	}
	if httpResp.StatusCode != http.StatusOK {
		err = errors.Errorf("Failed to remove capacity for resourceAlias: %s, status code: %d", resourceAlias, httpResp.StatusCode)
		return
	}
	defer func() {
		if closeErr := httpResp.Body.Close(); closeErr != nil {
			err = errors.Wrapf(closeErr, "Failed to close response body")
		}
	}()
	body, err := io.ReadAll(httpResp.Body)
	if err != nil {
		err = errors.Wrapf(err, "Failed read response body when removing capacity for resourceAlias: %s", resourceAlias)
		return
	}
	err = json.Unmarshal(body, &resp)
	if err != nil {
		err = errors.Wrapf(err, "Failed unmarshal response body when removing capacity for resourceAlias: %s", resourceAlias)
		return
	}
	return resp, nil
}

func (c *ClientImpl) AddCapacities(ctx context.Context, changes []ResourceCapacityChange) (ResourceInstances, error) {
	return c.changeCapacities(ctx, addCapacitiesPath, changes, "add")
}

func (c *ClientImpl) RemoveCapacities(ctx context.Context, changes []ResourceCapacityChange) (ResourceInstances, error) {
	return c.changeCapacities(ctx, removeCapacitiesPath, changes, "remove")
}

func (c *ClientImpl) changeCapacities(ctx context.Context, path string, changes []ResourceCapacityChange, operation string) (resp ResourceInstances, err error) {
	if len(changes) == 0 {
		return ResourceInstances{}, nil
	}

	if c.config.DryRun {
		resources := make([]ResourceInstance, 0, len(changes))
		for _, change := range changes {
			resources = append(resources, ResourceInstance{
				InstanceID:    "instance-abc",
				ResourceID:    "resource-abc",
				ResourceAlias: change.ResourceAlias,
			})
		}
		return ResourceInstances{InstanceID: "instance-abc", Resources: resources}, nil
	}

	reqBody := map[string][]ResourceCapacityChange{
		"resources": changes,
	}
	reqBytes, err := json.Marshal(reqBody)
	if err != nil {
		return resp, errors.Wrapf(err, "Failed marshal request body when attempting grouped capacity %s", operation)
	}

	req, err := retryablehttp.NewRequestWithContext(ctx, http.MethodPost, c.urlFor(path), reqBytes)
	if err != nil {
		return resp, err
	}
	req.Header.Add("Content-Type", "application/json")

	httpResp, err := c.httpClient.Do(req)
	if err != nil {
		return resp, errors.Wrapf(err, "Failed grouped capacity %s request", operation)
	}
	if httpResp.StatusCode != http.StatusOK {
		return resp, errors.Errorf("Failed grouped capacity %s request, status code: %d", operation, httpResp.StatusCode)
	}
	defer func() {
		if closeErr := httpResp.Body.Close(); closeErr != nil {
			err = errors.Wrapf(closeErr, "Failed to close response body")
		}
	}()

	body, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return resp, errors.Wrapf(err, "Failed read response body after grouped capacity %s", operation)
	}
	err = json.Unmarshal(body, &resp)
	if err != nil {
		return resp, errors.Wrapf(err, "Failed unmarshal response body after grouped capacity %s", operation)
	}
	return resp, nil
}

func (c *ClientImpl) urlFor(path string) string {
	return c.baseURL + path
}
