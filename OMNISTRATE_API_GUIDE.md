# Omnistrate Multi-Resource Capacity API Guide

This guide documents the monitoring sidecar calls used by this example. The controller talks to the sidecar on localhost from inside the Omnistrate instance.

Base URL:

```text
http://127.0.0.1:49750
```

## Grouped Capacity Changes

Use grouped capacity changes when a scaling decision needs to modify more than one resource in the same instance.

### Add Capacity

```http
POST /resources/capacity/add
Content-Type: application/json
```

```json
{
  "resources": [
    {
      "resourceAlias": "api",
      "capacityToBeAdded": 2
    },
    {
      "resourceAlias": "worker",
      "capacityToBeAdded": 1
    }
  ]
}
```

### Remove Capacity

```http
POST /resources/capacity/remove
Content-Type: application/json
```

```json
{
  "resources": [
    {
      "resourceAlias": "api",
      "capacityToBeRemoved": 1
    },
    {
      "resourceAlias": "worker",
      "capacityToBeRemoved": 2
    }
  ]
}
```

Expected response shape:

```json
{
  "instanceId": "instance-1",
  "resources": [
    {
      "resourceAlias": "api",
      "resourceId": "resource-api"
    },
    {
      "resourceAlias": "worker",
      "resourceId": "resource-worker"
    }
  ]
}
```

## Capacity Status

The controller reads each resource's current capacity before building a grouped request.

```http
GET /resource/{resourceAlias}/capacity
```

Expected response shape:

```json
{
  "instanceId": "instance-1",
  "resourceId": "resource-api",
  "resourceAlias": "api",
  "status": "ACTIVE",
  "currentCapacity": 2,
  "lastObservedTimestamp": "2026-06-17T12:00:00Z"
}
```

The controller only sends grouped add/remove requests for resources that are currently `ACTIVE`.

## Single-Resource Sidecar API

The controller uses the single-resource sidecar paths when a scale request targets one configured resource:

```http
POST /resource/{resourceAlias}/capacity/add
POST /resource/{resourceAlias}/capacity/remove
GET  /resource/{resourceAlias}/capacity
```

Single-resource add request:

```json
{
  "capacityToBeAdded": 1
}
```

Single-resource remove request:

```json
{
  "capacityToBeRemoved": 1
}
```

The controller uses these paths when the caller sends the single-resource shorthand request body:

```json
{
  "targetCapacity": 2
}
```

## Controller API

### Multi-Resource Pattern

```http
POST /scale
Content-Type: application/json
```

```json
{
  "targets": {
    "api": 3,
    "worker": 2
  }
}
```

Every key in `targets` must be listed in `AUTOSCALER_TARGET_RESOURCES`.

### Single-Resource Shorthand

```http
POST /scale
Content-Type: application/json
```

```json
{
  "targetCapacity": 2
}
```

This scales the first configured resource from `AUTOSCALER_TARGET_RESOURCES`.

## Implementation Notes

- `AUTOSCALER_TARGET_RESOURCES=api,worker` enables grouped scaling.
- `AUTOSCALER_STEPS` caps the change per resource in each grouped request.
- The controller may send one add batch and one remove batch for the same scaling decision.
- Service orchestration applies each requested resource change in parallel on the platform side.
- The controller rejects unknown resource aliases before calling the sidecar.

## Test Coverage

This example includes tests for:

- grouped sidecar payloads for add and remove.
- `AUTOSCALER_TARGET_RESOURCES` parsing.
- single-resource shorthand behavior.
- multi-resource target validation and grouped batching.
