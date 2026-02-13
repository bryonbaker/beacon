# Beacon Configuration Reference

Beacon is configured via a YAML file (default location: `/config/config.yaml`, overridable with the `CONFIG_PATH` environment variable). Configuration is read once at startup; changes require a pod restart.

Sensitive values are provided via environment variables and are never read from the YAML file.

## Configuration File Reference

### Application Settings (`app`)

| Field | Type | Default | Description |
|---|---|---|---|
| `app.name` | string | (none) | Application name, included in logs and metrics. |
| `app.version` | string | (none) | Application version, included in the `User-Agent` header of notification requests. |
| `app.logLevel` | string | `"info"` | Log verbosity level. One of: `debug`, `info`, `warn`, `error`. |
| `app.logFormat` | string | `"json"` | Log output format. One of: `json` (structured, for production), `text` (human-readable, for development). |

### Resources to Watch (`resources`)

| Field | Type | Default | Description |
|---|---|---|---|
| `resources` | array | (required) | List of Kubernetes resource types to watch. At least one must be configured. |
| `resources[].apiVersion` | string | (required) | Kubernetes API version of the resource (e.g. `v1`, `apps/v1`, `maas.io/v1`). |
| `resources[].kind` | string | (required) | Kubernetes resource kind (e.g. `Pod`, `Deployment`, `Machine`). |
| `resources[].namespaces` | []string | (all namespaces) | List of namespaces to watch. If empty or omitted, all namespaces are watched. |

### Annotation Filter (`annotation`)

| Field | Type | Default | Description |
|---|---|---|---|
| `annotation.key` | string | `"bakerapps.net.maas"` | The annotation key to look for on Kubernetes resources. Only resources carrying this annotation are tracked. |
| `annotation.values` | []string | (any value) | If specified, only resources whose annotation value matches one of these values are tracked. If empty or omitted, any annotation value is accepted. |

### Endpoint Configuration (`endpoint`)

| Field | Type | Default | Description |
|---|---|---|---|
| `endpoint.url` | string | (required) | The HTTP URL of the notification endpoint. Must be a fully-qualified URL (e.g. `https://example.com/api/notify`). |
| `endpoint.method` | string | `"POST"` | HTTP method for notification requests. One of: `POST`, `PUT`, `PATCH`. |
| `endpoint.timeout` | duration | `"30s"` | Timeout for each individual notification HTTP request. |
| `endpoint.headers` | map[string]string | (none) | Additional HTTP headers to include in notification requests. |

### Endpoint Retry Configuration (`endpoint.retry`)

| Field | Type | Default | Description |
|---|---|---|---|
| `endpoint.retry.maxAttempts` | int | `10` | Maximum number of notification delivery attempts before giving up. |
| `endpoint.retry.initialBackoff` | duration | `"1s"` | Initial backoff duration before the first retry. |
| `endpoint.retry.maxBackoff` | duration | `"5m"` | Maximum backoff duration cap. |
| `endpoint.retry.backoffMultiplier` | float | `2.0` | Multiplier applied to the backoff duration after each attempt. Formula: `min(initialBackoff * multiplier^attempt, maxBackoff)`. |
| `endpoint.retry.jitter` | float | `0.1` | Jitter factor (0.0 to 1.0). A value of 0.1 means +/-10% random variation on the computed backoff. |

### Endpoint TLS Configuration (`endpoint.tls`)

| Field | Type | Default | Description |
|---|---|---|---|
| `endpoint.tls.insecureSkipVerify` | bool | `false` | If true, skip TLS certificate verification. Use only for development/testing. |
| `endpoint.tls.caFile` | string | (none) | Path to a CA certificate file for verifying the endpoint's TLS certificate. |

### Worker Configuration (`worker`)

| Field | Type | Default | Description |
|---|---|---|---|
| `worker.pollInterval` | duration | `"5s"` | How often the notification worker polls the database for pending notifications. |
| `worker.batchSize` | int | `10` | Maximum number of pending notifications to fetch per poll cycle. |
| `worker.concurrency` | int | `5` | Maximum number of concurrent notification deliveries. |

### Reconciliation Configuration (`reconciliation`)

| Field | Type | Default | Description |
|---|---|---|---|
| `reconciliation.enabled` | bool | `true` | Whether the periodic reconciliation loop is enabled. |
| `reconciliation.interval` | duration | `"15m"` | How often the reconciler runs to compare cluster state against database state. |
| `reconciliation.onStartup` | bool | `true` | Whether to run a reconciliation pass immediately at application startup. |
| `reconciliation.timeout` | duration | `"10m"` | Timeout for each reconciliation run. |

### Retention Configuration (`retention`)

| Field | Type | Default | Description |
|---|---|---|---|
| `retention.enabled` | bool | `true` | Whether the periodic cleanup job is enabled. |
| `retention.cleanupInterval` | duration | `"1h"` | How often the cleanup job runs. |
| `retention.retentionPeriod` | duration | `"48h"` | How long to keep fully-processed records (deleted + notified) before removing them. |

### Storage Configuration (`storage`)

| Field | Type | Default | Description |
|---|---|---|---|
| `storage.dbPath` | string | `"/data/events.db"` | File path for the SQLite database. |
| `storage.volumePath` | string | `"/data"` | Mount path of the persistent volume (used for storage monitoring). |
| `storage.monitorInterval` | duration | `"1m"` | How often the storage monitor checks volume and database metrics. |
| `storage.warningThreshold` | int | `80` | Volume usage percentage at which a warning-level storage pressure alert is raised. |
| `storage.criticalThreshold` | int | `90` | Volume usage percentage at which a critical-level storage pressure alert is raised. |

### Metrics Configuration (`metrics`)

| Field | Type | Default | Description |
|---|---|---|---|
| `metrics.enabled` | bool | `true` | Whether Prometheus metrics are exposed. |
| `metrics.port` | int | `8080` | TCP port for the metrics/health HTTP server. |
| `metrics.path` | string | `"/metrics"` | HTTP path where Prometheus metrics are served. |

### Health Configuration (`health`)

| Field | Type | Default | Description |
|---|---|---|---|
| `health.livenessPath` | string | `"/healthz"` | HTTP path for the Kubernetes liveness probe. |
| `health.readinessPath` | string | `"/ready"` | HTTP path for the Kubernetes readiness probe. |
| `health.port` | int | `8080` | TCP port for health endpoints (shared with the metrics server). |

## Environment Variable Overrides

The following environment variables override YAML configuration values. They are applied after the YAML file is parsed and defaults are set.

| Variable | Overrides | Description |
|---|---|---|
| `CONFIG_PATH` | (startup) | Path to the YAML configuration file. Default: `/config/config.yaml`. |
| `DB_PATH` | `storage.dbPath` | Path to the SQLite database file. |
| `ENDPOINT_URL` | `endpoint.url` | Notification endpoint URL. |
| `ENDPOINT_AUTH_TOKEN` | (auth) | Bearer token for endpoint authentication. Set via Kubernetes Secret. Never stored in the YAML file. |

## Duration Format

Duration values use Go's duration string format: a sequence of decimal numbers with time unit suffixes. Valid units are `ns` (nanosecond), `us`/`ms` (microsecond/millisecond), `s` (second), `m` (minute), `h` (hour).

Examples: `"30s"`, `"5m"`, `"1h30m"`, `"100ms"`, `"48h"`.

## Example Configuration

```yaml
app:
  name: beacon
  version: "1.0.0"
  logLevel: info
  logFormat: json

resources:
  - apiVersion: v1
    kind: Pod
    namespaces:
      - production
      - staging
  - apiVersion: maas.io/v1
    kind: Machine

annotation:
  key: bakerapps.net.maas
  values:
    - managed
    - tracked

endpoint:
  url: https://api.example.com/events
  method: POST
  timeout: 30s
  retry:
    maxAttempts: 10
    initialBackoff: 1s
    maxBackoff: 5m
    backoffMultiplier: 2.0
    jitter: 0.1
  headers:
    X-Source: beacon
  tls:
    insecureSkipVerify: false
    caFile: /etc/ssl/certs/ca.pem

worker:
  pollInterval: 5s
  batchSize: 10
  concurrency: 5

reconciliation:
  enabled: true
  interval: 15m
  onStartup: true
  timeout: 10m

retention:
  enabled: true
  cleanupInterval: 1h
  retentionPeriod: 48h

storage:
  dbPath: /data/events.db
  volumePath: /data
  monitorInterval: 1m
  warningThreshold: 80
  criticalThreshold: 90

metrics:
  enabled: true
  port: 8080
  path: /metrics

health:
  livenessPath: /healthz
  readinessPath: /ready
  port: 8080
```
