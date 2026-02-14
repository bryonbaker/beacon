# Beacon Configuration Reference

Beacon is configured via a YAML file (default location: `/config/config.yaml`, overridable with the `CONFIG_PATH` environment variable). Configuration is read once at startup; changes require a pod restart.

Sensitive values are provided via environment variables and are never read from the YAML file.

---

## Configuration File Reference

### Application Settings (`app`)

General application identity and logging behaviour.

| Field | Type | Default | Description |
|---|---|---|---|
| `app.name` | string | (none) | Application name, included in logs and metrics. |
| `app.version` | string | (none) | Application version, included in the `User-Agent` header (`beacon/{version}`) of notification requests. |
| `app.logLevel` | string | `"info"` | Log verbosity level. One of: `debug`, `info`, `warn`, `error`. |
| `app.logFormat` | string | `"json"` | Log output format. One of: `json` (structured, for production), `text` (human-readable, for development). |

### Resources to Watch (`resources`)

Defines which Kubernetes resource types Beacon monitors. At least one resource must be configured. Each entry describes a single API resource kind.

| Field | Type | Default | Description |
|---|---|---|---|
| `resources` | array | (required) | List of Kubernetes resource types to watch. At least one must be configured. |
| `resources[].apiVersion` | string | (required) | Kubernetes API group and version (e.g. `v1`, `apps/v1`, `serving.kserve.io/v1alpha1`). Core resources use `v1`. |
| `resources[].kind` | string | (required) | Kubernetes resource kind (e.g. `Pod`, `ConfigMap`, `LLMInferenceService`). |
| `resources[].resource` | string | (none) | Plural resource name for the Kubernetes API (e.g. `llminferenceservices`). Required for custom resources where the plural form cannot be inferred from the kind. Core resources like `Pod` do not need this. |
| `resources[].namespaces` | []string | (all namespaces) | List of namespaces to watch. If empty or omitted, all namespaces are watched. |

Example with a core resource and a custom resource:

```yaml
resources:
  - apiVersion: v1
    kind: Pod
    namespaces:
      - production
  - apiVersion: serving.kserve.io/v1alpha1
    kind: LLMInferenceService
    resource: llminferenceservices
    namespaces: []
```

### Annotation Filter (`annotation`)

Controls which resources are tracked based on the presence and value of a Kubernetes annotation. Only resources carrying the configured annotation are monitored.

| Field | Type | Default | Description |
|---|---|---|---|
| `annotation.key` | string | `"bakerapps.net.maas"` | The annotation key to look for on Kubernetes resources. Only resources carrying this annotation are tracked. |
| `annotation.values` | []string | (any value) | Accepted annotation values. If specified, only resources whose annotation value matches one of these strings are tracked. If empty or omitted, any annotation value is accepted. |

### Payload Content (`payload`)

Controls which Kubernetes annotations and labels from the watched resource are included in the notification payload's `data.metadata` section.

| Field | Type | Default | Description |
|---|---|---|---|
| `payload.annotations` | []string | (none) | List of annotation keys to extract from the resource and include in the payload. If empty or omitted, no annotations are included. |
| `payload.labels` | []string | (all labels) | List of label keys to include in the payload. If empty or omitted, all labels on the resource are included. If specified, only the listed label keys are included. |

Example:

```yaml
payload:
  annotations:
    - bakerapps.net/customer-id
    - bakerapps.net/account
  labels:
    - app
    - version
```

### CloudEvents Envelope (`cloudEvents`)

Beacon sends all notifications as [CloudEvents v1.0](https://github.com/cloudevents/spec/blob/v1.0.2/cloudevents/spec.md) envelopes using HTTP structured content mode (`Content-Type: application/cloudevents+json`). This section controls how the CloudEvents envelope attributes are constructed.

| Field | Type | Default | Description |
|---|---|---|---|
| `cloudEvents.source` | string | `"/beacon"` | URI-reference prefix for the CloudEvents `source` attribute. The full source is built as `{source}/{namespace}/{resourceType}` (e.g. `/beacon/default/Pod`). Use this to distinguish multiple Beacon instances reporting to the same endpoint. |
| `cloudEvents.typePrefix` | string | `"net.bakerapps.beacon.resource"` | Reverse-DNS prefix for the CloudEvents `type` attribute. The full type is built as `{typePrefix}.{eventType}` where `eventType` is `created` or `deleted` (e.g. `net.bakerapps.beacon.resource.created`). |

The following CloudEvents attributes are set automatically and are not configurable:

| Attribute | Value | Description |
|---|---|---|
| `specversion` | `"1.0"` | CloudEvents specification version. |
| `id` | managed object ID | Unique event identifier (UUID). |
| `subject` | resource name | The Kubernetes resource name (e.g. `my-pod`). |
| `time` | RFC 3339 timestamp | UTC timestamp of when the notification was built. |
| `datacontenttype` | `"application/json"` | Media type of the `data` field. |

Example payload sent to the endpoint:

```json
{
  "specversion": "1.0",
  "id": "550e8400-e29b-41d4-a716-446655440000",
  "source": "/beacon/default/LLMInferenceService",
  "type": "net.bakerapps.beacon.resource.created",
  "subject": "my-service",
  "time": "2025-06-15T10:30:00Z",
  "datacontenttype": "application/json",
  "data": {
    "resource": {
      "uid": "k8s-uid-123",
      "type": "LLMInferenceService",
      "name": "my-service",
      "namespace": "default",
      "annotationValue": "true"
    },
    "metadata": {
      "annotations": { "bakerapps.net/customer-id": "C-12345" },
      "labels": { "app": "my-service" },
      "resourceVersion": "789"
    }
  }
}
```

### Endpoint Configuration (`endpoint`)

Configures the HTTP endpoint where notifications are delivered.

| Field | Type | Default | Description |
|---|---|---|---|
| `endpoint.url` | string | (required) | The HTTP URL of the notification endpoint. Must be a fully-qualified URL (e.g. `https://example.com/api/notify`). Can be overridden by the `ENDPOINT_URL` environment variable. |
| `endpoint.method` | string | `"POST"` | HTTP method for notification requests. One of: `POST`, `PUT`, `PATCH`. |
| `endpoint.timeout` | duration | `"30s"` | Timeout for each individual notification HTTP request. |
| `endpoint.headers` | map[string]string | (none) | Additional HTTP headers to include in notification requests (e.g. `X-Source: beacon`). Note: the `Content-Type` header is always set to `application/cloudevents+json; charset=UTF-8` and cannot be overridden via this field. |

### Endpoint Retry Configuration (`endpoint.retry`)

Controls exponential backoff retry behaviour for failed notification deliveries. Retries apply to network errors and retriable HTTP status codes (408, 429, 500, 502, 503, 504). Non-retriable client errors (400, 401, 403, 404, 422) cause permanent failure without retry.

| Field | Type | Default | Description |
|---|---|---|---|
| `endpoint.retry.maxAttempts` | int | `10` | Maximum number of delivery attempts before the notification is marked as permanently failed. |
| `endpoint.retry.initialBackoff` | duration | `"1s"` | Backoff duration before the first retry attempt. |
| `endpoint.retry.maxBackoff` | duration | `"5m"` | Upper bound on backoff duration. The computed backoff is capped at this value regardless of the attempt count. |
| `endpoint.retry.backoffMultiplier` | float | `2.0` | Multiplier applied to the backoff duration after each attempt. Formula: `min(initialBackoff * multiplier^attempt, maxBackoff)`. |
| `endpoint.retry.jitter` | float | `0.1` | Random variation factor (0.0 to 1.0) applied to the computed backoff to prevent thundering-herd effects. A value of `0.1` means +/-10% random variation. |

With the defaults, the retry sequence is approximately: 1s, 2s, 4s, 8s, 16s, 32s, 64s, 128s, 256s, 300s (capped).

### Endpoint TLS Configuration (`endpoint.tls`)

| Field | Type | Default | Description |
|---|---|---|---|
| `endpoint.tls.insecureSkipVerify` | bool | `false` | If `true`, skip TLS certificate verification. Use only for development/testing. |
| `endpoint.tls.caFile` | string | (none) | Path to a PEM-encoded CA certificate file for verifying the endpoint's TLS certificate. If omitted, the system certificate pool is used. |

### Worker Configuration (`worker`)

Controls the notification delivery worker that polls the database for pending events.

| Field | Type | Default | Description |
|---|---|---|---|
| `worker.pollInterval` | duration | `"5s"` | How often the worker polls the database for pending notifications. Lower values reduce delivery latency but increase database load. |
| `worker.batchSize` | int | `10` | Maximum number of pending notifications fetched per poll cycle. |
| `worker.concurrency` | int | `5` | Maximum number of concurrent notification deliveries. |

### Reconciliation Configuration (`reconciliation`)

The reconciler periodically compares the live cluster state against the database to detect events that may have been missed during watch disconnections or downtime.

| Field | Type | Default | Description |
|---|---|---|---|
| `reconciliation.enabled` | bool | `true` | Whether the periodic reconciliation loop is enabled. |
| `reconciliation.interval` | duration | `"15m"` | How often the reconciler runs. |
| `reconciliation.onStartup` | bool | `true` | Whether to run a reconciliation pass immediately at application startup, catching any events missed while the pod was down. |
| `reconciliation.timeout` | duration | `"10m"` | Timeout for each reconciliation run. Should be shorter than `interval` to prevent overlapping runs. |

### Retention Configuration (`retention`)

Controls automatic cleanup of old records from the SQLite database. Only records that are in the `deleted` state with both creation and deletion notifications successfully sent are eligible for cleanup.

| Field | Type | Default | Description |
|---|---|---|---|
| `retention.enabled` | bool | `true` | Whether the periodic cleanup job is enabled. |
| `retention.cleanupInterval` | duration | `"1h"` | How often the cleanup job runs. |
| `retention.retentionPeriod` | duration | `"48h"` | Minimum age of a fully-processed record before it is eligible for deletion. Measured from the time the resource was marked as deleted. |

### Storage Configuration (`storage`)

Controls the SQLite database location and persistent volume monitoring. The storage monitor periodically checks filesystem usage and emits Prometheus metrics and log warnings when usage exceeds the configured thresholds.

| Field | Type | Default | Description |
|---|---|---|---|
| `storage.dbPath` | string | `"/data/events.db"` | File path for the SQLite database. Can be overridden by the `DB_PATH` environment variable. |
| `storage.volumePath` | string | `"/data"` | Mount path of the persistent volume. Used by the storage monitor to check filesystem usage. |
| `storage.monitorInterval` | duration | `"1m"` | How often the storage monitor checks volume and database size metrics. |
| `storage.warningThreshold` | int | `80` | Volume usage percentage (0-100) at which a warning-level log is emitted. |
| `storage.criticalThreshold` | int | `90` | Volume usage percentage (0-100) at which a critical-level log is emitted. |

### Metrics Configuration (`metrics`)

Controls the Prometheus metrics HTTP server.

| Field | Type | Default | Description |
|---|---|---|---|
| `metrics.enabled` | bool | `true` | Whether the Prometheus metrics endpoint is exposed. |
| `metrics.port` | int | `8080` | TCP port for the metrics and health HTTP server. |
| `metrics.path` | string | `"/metrics"` | HTTP path where Prometheus metrics are served. |

### Health Configuration (`health`)

Controls the Kubernetes liveness and readiness probe endpoints. These are served on the same HTTP server as metrics.

| Field | Type | Default | Description |
|---|---|---|---|
| `health.livenessPath` | string | `"/healthz"` | HTTP path for the Kubernetes liveness probe. Returns 200 when the process is running. |
| `health.readinessPath` | string | `"/ready"` | HTTP path for the Kubernetes readiness probe. Returns 200 when the service is ready to process events. |
| `health.port` | int | `8080` | TCP port for health endpoints (shared with the metrics server). |

---

## Environment Variable Overrides

The following environment variables override YAML configuration values. They are applied after the YAML file is parsed and defaults are set.

| Variable | Overrides | Description |
|---|---|---|
| `CONFIG_PATH` | (startup) | Path to the YAML configuration file. Default: `/config/config.yaml`. |
| `DB_PATH` | `storage.dbPath` | Path to the SQLite database file. |
| `ENDPOINT_URL` | `endpoint.url` | Notification endpoint URL. Useful for injecting the URL without modifying the ConfigMap. |
| `ENDPOINT_AUTH_TOKEN` | (auth) | Bearer token for endpoint authentication. Sent as the `Authorization: Bearer {token}` header on every notification request. Set via a Kubernetes Secret. This value is never read from the YAML file. |

---

## Duration Format

Duration values use Go's duration string format: a sequence of decimal numbers with time unit suffixes. Valid units are `ns` (nanosecond), `us`/`ms` (microsecond/millisecond), `s` (second), `m` (minute), `h` (hour).

Examples: `"30s"`, `"5m"`, `"1h30m"`, `"100ms"`, `"48h"`.

---

## Complete Example

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
  - apiVersion: serving.kserve.io/v1alpha1
    kind: LLMInferenceService
    resource: llminferenceservices
    namespaces: []

annotation:
  key: bakerapps.net/maas
  values:
    - "true"

payload:
  annotations:
    - bakerapps.net/customer-id
    - bakerapps.net/account
  labels:
    - app
    - version

cloudEvents:
  source: "/beacon"
  typePrefix: "net.bakerapps.beacon.resource"

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
