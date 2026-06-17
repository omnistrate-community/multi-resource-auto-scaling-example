# Multi-Resource Custom Auto Scaling Example for Omnistrate

This repository is a reference implementation for custom autoscaling that can change capacity for multiple Omnistrate resources in the same instance at the same time.

It is based on the single-resource custom autoscaling pattern, but adds:

- `AUTOSCALER_TARGET_RESOURCES` for comma-separated resource aliases.
- `POST /scale` support for grouped targets via `{"targets":{"api":3,"worker":2}}`.
- grouped monitoring sidecar requests to `/resources/capacity/add` and `/resources/capacity/remove`.
- backward compatibility for the old single-resource env var and request body.
- unit coverage for both the old single-resource path and the new grouped path.

## Architecture

The controller runs inside the Omnistrate instance and talks to the local monitoring sidecar at `http://127.0.0.1:49750`.

1. Your policy decides target capacity for one or more resources.
2. The controller checks each requested resource's current capacity.
3. The controller validates that every requested resource is configured in `AUTOSCALER_TARGET_RESOURCES`.
4. The controller builds add and remove batches.
5. The controller sends at most one grouped add request and one grouped remove request to the monitoring sidecar.
6. Omnistrate service orchestration applies the requested capacity changes to the named resources.

Single-resource callers can continue to use `AUTOSCALER_TARGET_RESOURCE` and `{"targetCapacity":2}`.

## Configuration

| Variable | Description | Default | Required |
|----------|-------------|---------|----------|
| `AUTOSCALER_TARGET_RESOURCES` | Comma-separated resource aliases to scale, for example `api,worker` | - | Preferred |
| `AUTOSCALER_TARGET_RESOURCE` | Legacy single resource alias. Used when `AUTOSCALER_TARGET_RESOURCES` is unset | - | Backward compatible fallback |
| `AUTOSCALER_COOLDOWN` | Cooldown period in seconds between scaling operations | 300 | No |
| `AUTOSCALER_STEPS` | Max capacity units to add/remove per resource per operation | 1 | No |
| `AUTOSCALER_WAIT_FOR_ACTIVE_TIMEOUT` | Max time to wait for a resource to become `ACTIVE`, in seconds | 900 | No |
| `AUTOSCALER_WAIT_FOR_ACTIVE_CHECK_INTERVAL` | Interval between status checks, in seconds | 30 | No |
| `DRY_RUN` | Enable dry-run mode without sidecar calls | false | No |
| `PORT` | HTTP server port | 3000 | No |

## Example Compose

```yaml
services:
  controller:
    depends_on:
      - api
      - worker
    image: ghcr.io/omnistrate-community/multi-resource-auto-scaling-example:0.0.1
    ports:
      - '3000:3000'
    environment:
      - AUTOSCALER_COOLDOWN=300
      - AUTOSCALER_TARGET_RESOURCES=api,worker
      - AUTOSCALER_STEPS=1

  api:
    x-omnistrate-mode-internal: true
    x-omnistrate-capabilities:
      autoscaling:
        policyType: custom
        maxReplicas: 5
        minReplicas: 1
    image: hashicorp/http-echo:1.0.0
    command: ['-text=api resource']

  worker:
    x-omnistrate-mode-internal: true
    x-omnistrate-capabilities:
      autoscaling:
        policyType: custom
        maxReplicas: 3
        minReplicas: 0
    image: busybox:1.37.0
    command: ['sh', '-c', 'while true; do echo Working...; sleep 10; done']
```

## Controller API

### Multi-Resource Scale

```bash
curl -X POST http://localhost:3000/scale \
  -H 'Content-Type: application/json' \
  -d '{"targets":{"api":3,"worker":2}}'
```

The controller will compare each target against current capacity and send grouped sidecar events:

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

### Legacy Single-Resource Scale

This request remains supported:

```bash
curl -X POST http://localhost:3000/scale \
  -H 'Content-Type: application/json' \
  -d '{"targetCapacity":2}'
```

When `AUTOSCALER_TARGET_RESOURCES` is unset, the controller falls back to `AUTOSCALER_TARGET_RESOURCE`.

### Status

```bash
curl http://localhost:3000/status
```

With multiple target resources configured, status returns per-resource capacity and lifecycle state. With one configured resource, the response keeps the existing single-resource shape.

## Local Development

```bash
make build
make unit-test
```

Run locally in dry-run mode:

```bash
make run
```

## Tests

The repo includes focused coverage for:

- plural `AUTOSCALER_TARGET_RESOURCES` parsing.
- fallback from `AUTOSCALER_TARGET_RESOURCE`.
- grouped add/remove sidecar payloads.
- multi-resource autoscaler validation and batching.
- existing single-resource autoscaler behavior.

Run everything:

```bash
go test ./...
```

## Files to Start With

- `cmd/controller.go`: REST API and status handlers.
- `internal/config/config.go`: env parsing and backward compatibility.
- `internal/autoscaler/autoscaler.go`: single-resource and multi-resource scaling logic.
- `internal/omnistrate_api/client.go`: monitoring sidecar API client.
- `omnistrate-compose.yaml`: runnable Omnistrate service example.

## License

See [LICENSE](LICENSE).
