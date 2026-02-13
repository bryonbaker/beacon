# Beacon

## Overview

Beacon is a Kubernetes event notification service that watches for annotated resources, persists events locally in SQLite, and delivers notifications to external HTTP endpoints with guaranteed delivery. It is designed to run as a single-replica pod inside a Kubernetes or OpenShift cluster and provides comprehensive observability through Prometheus metrics and Grafana dashboards.

The service monitors Kubernetes resources for the presence of the `bakerapps.net.maas` annotation. When an annotated resource is created, updated (annotation added or removed), or deleted, Beacon records the event in a local SQLite database and delivers a JSON notification to a configurable HTTP endpoint. A background reconciliation loop detects events that may have been missed during downtime, and a cleanup job removes stale records after a configurable retention period.

## Features

- **Real-time event detection** using Kubernetes informer watch pattern for low-latency resource tracking.
- **Guaranteed delivery** with local SQLite persistence; events are never lost even if the notification endpoint is temporarily unavailable.
- **Exponential backoff retry** with configurable jitter for transient endpoint failures (HTTP 408, 429, 500, 502, 503, 504).
- **Non-retriable failure handling** that preserves records for operator review when the endpoint returns client errors (HTTP 400, 401, 403, 404, 422).
- **Annotation mutation detection** that treats annotation additions as creation events and annotation removals as deletion events.
- **Background reconciliation** that periodically compares cluster state against the database to detect missed creations and deletions.
- **Configurable retention and cleanup** that removes fully-processed records after a configurable period while preserving failed notifications.
- **Storage monitoring** with volume usage and inode pressure alerts.
- **Prometheus metrics** for every component: watcher, notifier, reconciler, cleaner, database, and storage.
- **Health and readiness probes** for Kubernetes liveness and readiness checks.
- **Structured JSON logging** via zap for production-grade log analysis.

## Architecture

Beacon consists of seven cooperating components that run as goroutines within a single process:

```
+------------------+     +------------------+     +------------------+
|  Event Watcher   |---->|   SQLite DB      |<----|  Notification    |
| (Informers)      |     | (WAL mode)       |     |  Worker          |
+------------------+     +------------------+     +------------------+
                                |   ^                     |
                                |   |                     v
                          +-----+---+-----+     +------------------+
                          |               |     | HTTP Endpoint    |
                    +-----v----+  +-------+--+  | (external)       |
                    | Reconciler| | Cleaner   |  +------------------+
                    +----------+  +----------+
                          |
                    +-----v------+     +------------------+
                    | Storage    |     | Metrics Server   |
                    | Monitor    |     | (/metrics,       |
                    +------------+     |  /healthz,       |
                                       |  /ready)         |
                                       +------------------+
```

### Component Descriptions

| Component | Description |
|---|---|
| **Event Watcher** | Uses Kubernetes informers to watch configured resource types for add, update, and delete events. Filters by annotation presence and persists tracked objects to SQLite. |
| **SQLite Database** | Embedded database in WAL mode with incremental auto-vacuum. Stores all managed object state including notification tracking. Single-connection model for safe concurrent access. |
| **Notification Worker** | Polls the database for pending notifications and delivers HTTP POST requests to the configured endpoint. Implements exponential backoff retry for transient failures. |
| **Reconciliation Loop** | Runs periodically (default 15 minutes) and at startup. Compares cluster state against database state to detect missed creations and deletions. |
| **Cleanup Job** | Runs periodically (default 1 hour). Removes records that are deleted, fully notified, and older than the retention period. Runs incremental vacuum after cleanup. |
| **Storage Monitor** | Monitors SQLite database size, persistent volume usage, and inode consumption. Sets pressure indicators when configurable thresholds are exceeded. |
| **Metrics Server** | Serves Prometheus metrics at `/metrics`, liveness probe at `/healthz`, and readiness probe at `/ready` on a configurable port (default 8080). |

## Prerequisites

- **Go** 1.21 or later (build toolchain)
- **Kubernetes** 1.24 or later (or OpenShift 4.12+)
- **Podman** (container builds; Docker is also compatible)
- **SQLite** (embedded via go-sqlite3; CGO must be enabled)
- **Prometheus** (optional, for metrics collection)
- **Grafana** (optional, for dashboards)

## Quick Start

1. Clone the repository:

   ```bash
   git clone https://github.com/bryonbaker/beacon.git
   cd beacon/source
   ```

2. Build the binary:

   ```bash
   CGO_ENABLED=1 go build -o bin/beacon ./cmd/beacon
   ```

3. Create a configuration file (see `docs/configuration.md` for the full reference):

   ```bash
   cp internal/config/testdata/valid_config.yaml /tmp/config.yaml
   # Edit /tmp/config.yaml with your endpoint URL
   ```

4. Run locally (requires kubeconfig access to a cluster):

   ```bash
   CONFIG_PATH=/tmp/config.yaml ./bin/beacon
   ```

5. Build the container image:

   ```bash
   podman build -t beacon:latest .
   ```

## Configuration

Beacon is configured via a YAML file (default location: `/config/config.yaml`) with environment variable overrides for sensitive values. The configuration is read once at startup; changes require a pod restart.

Key configuration sections:

| Section | Purpose |
|---|---|
| `app` | Application name, version, log level, log format |
| `resources` | Kubernetes resource types to watch (apiVersion, kind, namespaces) |
| `annotation` | Annotation key and accepted values for filtering |
| `endpoint` | Notification HTTP endpoint URL, method, timeout, retry, headers, TLS |
| `worker` | Notification poll interval, batch size, concurrency |
| `reconciliation` | Reconciliation enabled, interval, startup run, timeout |
| `retention` | Cleanup enabled, interval, retention period |
| `storage` | Database path, volume path, monitoring interval, thresholds |
| `metrics` | Metrics enabled, port, path |
| `health` | Liveness and readiness probe paths and port |

See `docs/configuration.md` for the complete configuration reference with every field, type, default value, and description.

## Deployment

Beacon is deployed as a Kubernetes Deployment with a single replica and a PersistentVolumeClaim for the SQLite database. The deployment includes:

- A ConfigMap for the YAML configuration
- A Secret for the endpoint authentication token
- A PersistentVolumeClaim for the SQLite database
- A Deployment with liveness and readiness probes
- A Service for metrics scraping
- A ServiceMonitor for Prometheus Operator integration (optional)
- RBAC resources (ServiceAccount, ClusterRole, ClusterRoleBinding) for Kubernetes API access

See `docs/deployment.md` for the step-by-step deployment guide.

## Monitoring

Beacon exposes Prometheus metrics at `/metrics` (port 8080 by default) covering:

- Event detection counts and latency
- Notification delivery counts, duration, retry attempts, and failure codes
- Endpoint health (up/down, consecutive failures)
- Reconciliation run counts, duration, drift detection
- Cleanup run counts, records deleted, eligible records
- Database size, row counts, operation duration, errors
- Storage volume usage, inode usage, pressure indicators
- Component health and worker performance

A Grafana dashboard JSON is provided in the `grafana/` directory.

## Troubleshooting

See `docs/troubleshooting.md` for common issues and resolutions, including:

- Pod not reaching ready state
- Notifications not being delivered
- Database locked errors
- Watcher disconnection handling
- Storage pressure alerts

## Development

### Build

```bash
cd source
CGO_ENABLED=1 go build -o bin/beacon ./cmd/beacon
```

### Run Unit Tests

```bash
cd source
go test ./...
```

### Run Integration Tests

Integration tests use an in-memory SQLite database and httptest mock servers. They require the `integration` build tag:

```bash
cd source
CGO_ENABLED=1 go test -tags integration ./test/integration/ -v
```

### Project Structure

```
beacon/
  docs/                     Documentation
  source/
    cmd/beacon/     Application entry point
    internal/
      config/               Configuration loading and validation
      database/             Database interface and SQLite implementation
      watcher/              Kubernetes resource watching
      notifier/             Notification delivery with retry
      reconciler/           Periodic state reconciliation
      cleaner/              Record cleanup job
      storage/              Storage volume monitoring
      metrics/              Prometheus metrics and health server
      models/               Data structures
    pkg/kubernetes/         Kubernetes client construction
    test/integration/       Integration tests
    deployments/            Kubernetes manifests
    grafana/                Grafana dashboard definitions
    test-endpoint/          Test notification endpoint
```

### Contributing

1. Fork the repository.
2. Create a feature branch from `main`.
3. Make your changes with clear, descriptive commit messages.
4. Add unit tests for new functionality.
5. Run the full test suite including integration tests.
6. Submit a pull request with a description of the changes.
