# Beacon

A Kubernetes event notification service that watches for annotated resources, persists events to SQLite, and delivers HTTP notifications with guaranteed delivery.

Beacon monitors resources for the specified annotation (e.g. `bakerapps.net.maas`). When annotated resources are created, deleted, or have the annotation added/removed, Beacon records the event locally and delivers a JSON notification to a configurable endpoint with exponential backoff retry.

## Key Features

- Real-time event detection via Kubernetes informers
- Guaranteed delivery with local SQLite persistence
- Exponential backoff retry with jitter for transient failures
- Background reconciliation to catch events missed during downtime
- Prometheus metrics and Grafana dashboard
- Health/readiness probes for Kubernetes

## Quick Start

```bash
cd source/

# Build
make build

# Run tests
make test

# Build and push container image to quay.io/bryonbaker/beacon:latest
make image-build-push

# Deploy to Kubernetes
make deploy-dev
```

Before deploying, edit `source/deployments/configmap.yaml` to set your endpoint URL and `source/deployments/secret.yaml` to set your auth token.

## Project Structure

```
beacon/
├── requirements.md          # Requirements specification
├── docs/                    # Documentation
│   ├── README.md            # Detailed project overview
│   ├── architecture.md      # Component design and data flow
│   ├── configuration.md     # Complete config reference
│   ├── deployment.md        # Build, release, and deploy guide
│   └── troubleshooting.md   # Common issues and solutions
└── source/                  # All source code and build artifacts
    ├── cmd/beacon/          # Application entry point
    ├── internal/            # Core packages (config, database, watcher,
    │                        #   notifier, reconciler, cleaner, storage, metrics)
    ├── pkg/kubernetes/      # K8s client construction
    ├── deployments/         # Kubernetes manifests
    ├── grafana/             # Grafana dashboard JSON
    ├── test/integration/    # Integration tests
    ├── test-endpoint/       # Companion test HTTP endpoint
    ├── Containerfile        # Multi-stage container build
    └── Makefile             # Build, test, deploy targets
```

## Documentation

| Guide | Description |
|-------|-------------|
| [Architecture](docs/architecture.md) | Component design, data flow, failure handling |
| [Configuration](docs/configuration.md) | Every config field with types, defaults, and examples |
| [Deployment](docs/deployment.md) | Build, release, deploy, upgrade, backup/restore |
| [Troubleshooting](docs/troubleshooting.md) | Common issues with symptoms and resolutions |

## Prerequisites

- Go 1.21+ with CGO support
- Kubernetes 1.24+ or OpenShift 4.12+
- Podman (for container builds)
- SQLite (linked via `go-sqlite3`)

## License

See repository for license details.
