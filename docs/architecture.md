# Beacon Architecture

## Component Diagram

```
                     Kubernetes Cluster
                     +-----------------------------------------+
                     |                                         |
                     |  Annotated Resources                    |
                     |  (Pods, CRDs, etc.)                     |
                     |       |                                 |
                     +-------|---+-----------------------------+
                             |   |
                   Watch API |   | List API (reconciliation)
                             v   v
+----------------------------------------------------------------+
|                    Beacon Event Notifier                        |
|                                                                |
|  +------------------+                                          |
|  |  Event Watcher   |  Informer pattern                        |
|  |                  |  - AddFunc  -> handleAdd                 |
|  |  - Typed (Pods)  |  - UpdateFunc -> handleUpdate (mutation) |
|  |  - Dynamic (CRDs)|  - DeleteFunc -> handleDelete            |
|  +--------+---------+                                          |
|           |                                                    |
|           | InsertManagedObject / UpdateClusterState            |
|           v                                                    |
|  +------------------+                                          |
|  |  SQLite Database  |  WAL mode, single connection            |
|  |  managed_objects  |  Incremental auto-vacuum                |
|  |  table            |  Busy timeout: 5000ms                   |
|  +--+---+---+---+---+                                          |
|     |   |   |   |                                              |
|     |   |   |   +--------+                                     |
|     |   |   |            |                                     |
|     |   |   v            v                                     |
|     |   |  +-----------+ +-----------+                         |
|     |   |  | Reconciler| | Cleaner   |                         |
|     |   |  | (15m loop)| | (1h loop) |                         |
|     |   |  +-----------+ +-----------+                         |
|     |   |                                                      |
|     |   v                                                      |
|     |  +-------------------+                                   |
|     |  | Notification      |  Poll DB for pending              |
|     |  | Worker            |  Build CloudEvents envelope       |
|     |  | (5s poll, batch)  |  HTTP POST with retry             |
|     |  +--------+----------+                                   |
|     |           |                                              |
|     |           | HTTP POST                                    |
|     |           v                                              |
|     |  +-------------------+                                   |
|     |  | External Endpoint |  (configurable URL)               |
|     |  +-------------------+                                   |
|     |                                                          |
|     v                                                          |
|  +-------------------+    +-------------------+                |
|  | Storage Monitor   |    | Metrics Server    |                |
|  | (volume, inodes)  |    | :8080             |                |
|  +-------------------+    | /metrics          |                |
|                           | /healthz          |                |
|                           | /ready            |                |
|                           +-------------------+                |
+----------------------------------------------------------------+
```

## Data Flow

### Normal Operation: Resource Creation

1. A Kubernetes resource carrying the configured annotation (see `annotation.key` in the configuration) is created in the cluster.
2. The Event Watcher's informer fires an `AddFunc` callback.
3. The watcher checks for the configured annotation. If present, it extracts resource metadata and generates a UUID.
4. A `ManagedObject` record is inserted into the SQLite database with `cluster_state=exists`, `notified_created=false`, and `detection_source=watch`.
5. On the next poll cycle (every 5 seconds by default), the Notification Worker queries for pending notifications.
6. The worker builds a [CloudEvents v1.0](https://github.com/cloudevents/spec/blob/v1.0.2/cloudevents/spec.md) envelope in HTTP structured content mode (`Content-Type: application/cloudevents+json`) and sends an HTTP POST to the configured endpoint. The CloudEvents `type` attribute indicates the event kind (e.g. `net.bakerapps.beacon.resource.created`), and the business payload (resource metadata) is carried in the `data` field. See [Configuration](configuration.md#cloudevents-envelope-cloudevents) for the full envelope structure and configurable attributes.
7. On HTTP 2xx response, the worker updates `notified_created=true` and records the `created_notification_sent_at` timestamp.
8. The record remains in the database until the resource is deleted and fully notified.

### Normal Operation: Resource Deletion

1. The annotated resource is deleted from the cluster.
2. The Event Watcher's informer fires a `DeleteFunc` callback.
3. The watcher looks up the resource UID in the database. If found, it updates `cluster_state=deleted` and sets `deleted_at`.
4. On the next poll cycle, the Notification Worker picks up the record (pending deletion notification).
5. The worker sends a deletion notification and updates `notified_deleted=true`.
6. After the configurable retention period (default 48 hours), the Cleanup Job removes the record.

### Annotation Mutation Flow

1. When an existing Kubernetes resource has the annotation added via `kubectl annotate` or a controller update.
2. The Event Watcher's informer fires an `UpdateFunc` callback.
3. The watcher compares old and new annotations:
   - **Annotation added**: Old object lacks the annotation, new object has it. The watcher inserts a new `ManagedObject` with `detection_source=mutation` and logs a WARNING that the timestamp reflects mutation detection time.
   - **Annotation removed**: Old object has the annotation, new object does not. The watcher updates `cluster_state=deleted` and logs a WARNING.
4. Standard notification flow proceeds from this point.

### Reconciliation Flow

1. The Reconciliation Loop runs at startup (if configured) and periodically (default every 15 minutes).
2. For each configured resource type, the reconciler lists all resources in the cluster that carry the annotation.
3. It compares the cluster resource UIDs against the database:
   - **Missed creation**: A resource exists in the cluster with the annotation but is not in the database. The reconciler inserts a new record with `detection_source=reconciliation`.
   - **Missed deletion**: A resource exists in the database in `cluster_state=exists` but is no longer present in the cluster. The reconciler updates `cluster_state=deleted`.
4. All drift instances are logged at WARNING level and recorded in Prometheus metrics.

### Retry and Failure Flow

1. When the notification endpoint returns a retriable HTTP status code (408, 429, 500, 502, 503, 504) or a network error:
   - The worker increments `notification_attempts` and records `last_notification_attempt`.
   - The next retry is governed by exponential backoff: `min(initialBackoff * multiplier^attempt, maxBackoff) +/- jitter%`.
   - The record remains pending and will be picked up on subsequent poll cycles.
2. When the endpoint returns a non-retriable HTTP status code (400, 401, 403, 404, 422):
   - The full notification payload is logged at ERROR level for operator recovery.
   - The record is flagged with `notification_failed=true` and `notification_failed_code`.
   - The record is excluded from pending queries (no further retries).
   - The record is exempt from cleanup until manually resolved.

## Failure Handling

### Service Downtime

If Beacon is restarted or crashes:
- All events already recorded in SQLite are preserved (WAL mode ensures durability).
- On startup, the reconciliation loop detects any events that occurred during the downtime by comparing cluster state against the database.
- Pending notifications are picked up by the notification worker on the first poll cycle.

### Endpoint Unavailability

If the notification endpoint is unreachable:
- Notifications queue up in the database as pending records.
- The worker retries with exponential backoff, preventing endpoint overload.
- Prometheus metrics (`event_endpoint_up`, `event_endpoint_consecutive_failures`) provide visibility.
- When the endpoint recovers, queued notifications are delivered in order.

### Database Contention

SQLite is configured with:
- A single connection (`SetMaxOpenConns(1)`) to prevent WAL-mode contention.
- A busy timeout of 5000ms (`PRAGMA busy_timeout=5000`) to handle brief lock contention.
- WAL journal mode for concurrent read/write support.

### Watch Disconnection

Kubernetes informers handle watch disconnections automatically by re-establishing the watch connection and replaying missed events. The reconciliation loop provides an additional safety net by detecting any missed events periodically.

## Design Decisions

### SQLite as the Persistence Layer

**Decision**: Use SQLite instead of an external database (PostgreSQL, Redis, etc.) or message infrastructure such as Kafka.

**Rationale**:
- **Simplicity**: No external database or messaging platform dependency reduces operational complexity. Beacon runs as a single pod with a PersistentVolumeClaim.
- **Reliability**: SQLite's WAL mode provides ACID guarantees with crash recovery. The reconciliation loop ensures data is never lost even during unexpected restarts.
- **Performance**: For the expected throughput (up to 100 events/minute), SQLite with WAL mode easily meets the <100ms p95 latency requirement for database operations.
- **Portability**: The embedded database moves with the pod. Backup is a simple file copy.

**Trade-offs**:
- Single-replica constraint: Only one pod can access the database file safely.
- Storage limited to the PersistentVolumeClaim size.
- No built-in replication or high availability (mitigated by Kubernetes pod restart policies).

### Informer Pattern for Event Detection

**Decision**: Use Kubernetes informers (shared informer factory) instead of raw Watch API calls.

**Rationale**:
- **Efficiency**: Informers maintain a local cache and use delta-based updates, reducing API server load.
- **Reconnection**: Informers automatically handle watch disconnections and re-list operations.
- **Flexibility**: Typed informers for core API resources (Pods) and dynamic informers for CRDs, all through the same pattern.

### Guaranteed Delivery Approach

**Decision**: Persist events to SQLite before attempting notification delivery.

**Rationale**:
- **Durability**: Events are recorded immediately upon detection. Even if the notification endpoint is down, the event is not lost.
- **Idempotency**: The CloudEvents envelope includes the managed object ID (as the `id` attribute) and the resource UID (in the `data` payload), allowing the endpoint to deduplicate.
- **Decoupling**: The watcher and notifier are decoupled through the database. The watcher writes; the notifier reads and delivers. This allows each component to operate independently and at different speeds.

### Exponential Backoff with Jitter

**Decision**: Use exponential backoff with configurable jitter for retry timing.

**Rationale**:
- **Prevents thundering herd**: Jitter spreads retries across time, preventing multiple failed notifications from overwhelming the endpoint simultaneously.
- **Configurable**: Operators can tune the initial backoff, maximum backoff, multiplier, and jitter percentage to match their endpoint's recovery characteristics.
- **Industry standard**: This is the recommended retry strategy for distributed systems per AWS, Google Cloud, and other cloud provider guidelines.

### Separation of Non-Retriable Failures

**Decision**: Permanently flag records with `notification_failed=true` on non-retriable HTTP errors (4xx except 408/429) instead of retrying indefinitely.

**Rationale**:
- **Prevents wasted resources**: Retrying a 400 Bad Request indefinitely would waste CPU and network resources without any chance of success.
- **Preserves data**: The record remains in the database (not deleted) with the full payload logged at ERROR level, allowing operators to diagnose and recover manually.
- **Cleanup exemption**: Failed records are exempt from automatic cleanup, ensuring they are never silently lost.
