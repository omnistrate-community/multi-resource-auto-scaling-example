# Multi-Resource Custom Auto Scaling Example for Omnistrate

This repository is a reference implementation for custom autoscaling that changes capacity for multiple Omnistrate Resources in the same instance through grouped capacity operations.

It is based on the single-resource custom autoscaling pattern, but adds:

- `AUTOSCALER_TARGET_RESOURCES` for comma-separated resource aliases.
- `POST /scale` support for grouped targets via `{"targets":{"api":3,"worker":2}}`.
- grouped monitoring sidecar requests to `/resources/capacity/add` and `/resources/capacity/remove`.
- a single-resource shorthand request via `{"targetCapacity":2}`.
- progress polling so `/scale` waits for grouped capacity changes to advance before returning.

## Architecture

The controller runs inside the Omnistrate instance and talks to the local monitoring sidecar at `http://127.0.0.1:49750`.

1. Your policy decides target capacity for one or more resources.
2. The controller checks each requested resource's current capacity.
3. The controller validates that every requested resource is configured in `AUTOSCALER_TARGET_RESOURCES`.
4. The controller builds add and remove batches.
5. The controller sends at most one grouped add request and one grouped remove request to the monitoring sidecar for each scaling iteration.
6. Omnistrate service orchestration applies the requested capacity changes to the named Resources.
7. The controller polls capacity until every requested Resource reaches its target, or the configured timeout expires.

When the request body uses `{"targetCapacity":2}`, the controller scales the first alias listed in `AUTOSCALER_TARGET_RESOURCES`.

## Configuration

| Variable | Description | Default | Required |
|----------|-------------|---------|----------|
| `AUTOSCALER_TARGET_RESOURCES` | Comma-separated resource aliases to scale, for example `api,worker` | - | Yes |
| `AUTOSCALER_COOLDOWN` | Cooldown period in seconds between scaling operations | 300 | No |
| `AUTOSCALER_STEPS` | Max capacity units to add/remove per resource per operation | 1 | No |
| `AUTOSCALER_WAIT_FOR_ACTIVE_TIMEOUT` | Max time to wait for a resource to become `ACTIVE`, in seconds | 900 | No |
| `AUTOSCALER_WAIT_FOR_ACTIVE_CHECK_INTERVAL` | Interval between status checks, in seconds | 30 | No |
| `DRY_RUN` | Enable dry-run mode without sidecar calls | false | No |
| `PORT` | HTTP server port | 3000 | No |

## Example Compose

```yaml
x-omnistrate-load-balancer:
  https:
    - name: frontend
      description: L7 Load Balancer to expose the controller Web UI and API
      paths:
        - associatedResourceKey: controller
          path: /
          backendPort: 3000
        - associatedResourceKey: controller
          path: /scale
          backendPort: 3000
        - associatedResourceKey: controller
          path: /status
          backendPort: 3000

services:
  controller:
    depends_on:
      - api
      - worker
    image: ghcr.io/omnistrate-community/multi-resource-auto-scaling-example:0.0.15
    ports:
      - '3000:3000'
    environment:
      - AUTOSCALER_COOLDOWN=0
      - AUTOSCALER_TARGET_RESOURCES=api,worker
      - AUTOSCALER_STEPS=2
      - AUTOSCALER_WAIT_FOR_ACTIVE_TIMEOUT=1800
      - AUTOSCALER_WAIT_FOR_ACTIVE_CHECK_INTERVAL=15

  api:
    x-omnistrate-mode-internal: true
    x-omnistrate-capabilities:
      autoscaling:
        policyType: custom
        maxReplicas: 2
        minReplicas: 0
    image: hashicorp/http-echo:1.0.0
    command: ['-text=api resource']

  worker:
    x-omnistrate-mode-internal: true
    x-omnistrate-capabilities:
      autoscaling:
        policyType: custom
        maxReplicas: 2
        minReplicas: 0
    image: busybox:1.37.0
    command: ['sh', '-c', 'while true; do echo Working...; sleep 10; done']
```

## Controller API

The controller listens on port `3000` by default and exposes:

| Method | Path | Purpose |
|--------|------|---------|
| `POST` | `/scale` | Scale one Resource or several configured Resources to target capacities |
| `GET` | `/status` | Return current capacity and scaling state |
| `GET` | `/health` | Return a basic health response |

`POST /scale` is synchronous: the request can remain open while the controller waits for the capacity operation to make progress. The controller returns HTTP `409` if another scaling operation is already in progress.

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

Each target is validated against `AUTOSCALER_TARGET_RESOURCES`. A target may be zero when the Resource allows scaling to zero. If a target differs from the current capacity by more than `AUTOSCALER_STEPS`, the controller repeats grouped operations until the target is reached.

### Single-Resource Scale Shorthand

This request scales the first alias listed in `AUTOSCALER_TARGET_RESOURCES`:

```bash
curl -X POST http://localhost:3000/scale \
  -H 'Content-Type: application/json' \
  -d '{"targetCapacity":2}'
```

### Status

```bash
curl http://localhost:3000/status
```

With multiple target resources configured, status returns per-resource capacity and lifecycle state. With one configured resource, the response keeps the existing single-resource shape.

For the exact monitoring sidecar request and response payloads used by this example, see [OMNISTRATE_API_GUIDE.md](OMNISTRATE_API_GUIDE.md).

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
- grouped add/remove sidecar payloads.
- multi-resource target validation and batching.
- grouped scaling from zero replicas and progress polling.
- single-resource shorthand behavior.

Run everything:

```bash
go test ./...
```

## Files to Start With

- `cmd/controller.go`: REST API and status handlers.
- `internal/config/config.go`: env parsing.
- `internal/autoscaler/autoscaler.go`: single-resource and multi-resource scaling logic.
- `internal/omnistrate_api/client.go`: monitoring sidecar API client.
- `omnistrate-compose.yaml`: runnable Omnistrate service example.

## License

See [LICENSE](LICENSE).
