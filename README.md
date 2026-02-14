# Beacon

A Kubernetes event notification service that watches for annotated resources, persists events to SQLite, and delivers HTTP notifications with guaranteed delivery.

Beacon monitors Kubernetes resources for the presence of a configurable annotation. When annotated resources are created, deleted, or have the annotation added/removed, Beacon records the event locally and delivers a [CloudEvents v1.0](https://github.com/cloudevents/spec/blob/v1.0.2/cloudevents/spec.md) notification to a configurable endpoint with exponential backoff retry.

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

## AI Disclosure

This project includes portions generated or suggested by  artificial intelligence tools and subsequently reviewed,  modified, and validated by human contributors.
 
Human authorship, design decisions, and final responsibility for this code remain with the project contributors.

## License

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
