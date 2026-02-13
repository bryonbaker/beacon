# Beacon Troubleshooting Guide

## Pod Not Ready

### Symptoms

- Pod status shows `0/1 READY` or `CrashLoopBackOff`.
- Readiness probe fails with HTTP 503.
- Kubernetes events show `Readiness probe failed`.

### Possible Causes and Resolutions

**Cause 1: Configuration file not found or invalid**

The pod cannot start if the configuration file is missing or contains invalid YAML.

```bash
# Check pod logs for configuration errors
kubectl logs -n beacon -l app=beacon --tail=30

# Verify the ConfigMap is mounted correctly
kubectl describe pod -n beacon -l app=beacon | grep -A5 "Mounts"
kubectl exec -n beacon -l app=beacon -- cat /config/config.yaml
```

Resolution: Fix the ConfigMap content and restart the pod.

**Cause 2: Database path is not writable**

The pod cannot create or open the SQLite database if the PVC is not mounted or the path is read-only.

```bash
# Check for database-related errors
kubectl logs -n beacon -l app=beacon | grep -i "database\|sqlite\|failed to open"

# Verify the PVC is bound
kubectl get pvc -n beacon beacon-data
```

Resolution: Ensure the PVC is bound and the `storage.dbPath` directory exists on the volume. The volume must be writable. Check that `readOnlyRootFilesystem: true` in the security context does not affect the `/data` mount point.

**Cause 3: Kubernetes API unreachable**

If the pod cannot connect to the Kubernetes API server, the watcher fails to start and the readiness check fails.

```bash
# Check for Kubernetes client errors
kubectl logs -n beacon -l app=beacon | grep -i "kubernetes\|kube\|client"

# Verify the ServiceAccount has the correct RBAC permissions
kubectl auth can-i list pods --as=system:serviceaccount:beacon:beacon
```

Resolution: Verify the ServiceAccount, ClusterRole, and ClusterRoleBinding are correctly configured. Ensure the pod has network access to the API server.

**Cause 4: Missing endpoint URL**

The configuration validation requires `endpoint.url` to be set. If it is missing, the pod exits immediately.

```bash
kubectl logs -n beacon -l app=beacon | grep "endpoint.url"
```

Resolution: Set `endpoint.url` in the ConfigMap or via the `ENDPOINT_URL` environment variable.

---

## Notifications Not Being Delivered

### Symptoms

- The `event_notifications_pending_total` metric is increasing.
- The `event_endpoint_up` metric is 0.
- The `event_endpoint_consecutive_failures` metric is non-zero.
- Logs show repeated `retriable notification failure` or `notification request failed` messages.

### Possible Causes and Resolutions

**Cause 1: Endpoint is unreachable**

The notification endpoint is down, unreachable due to network policy, or the URL is incorrect.

```bash
# Check endpoint health metric
curl -s http://localhost:8080/metrics | grep event_endpoint_up

# Check logs for the specific error
kubectl logs -n beacon -l app=beacon | grep -i "notification.*fail"

# Test connectivity from the pod
kubectl exec -n beacon -l app=beacon -- curl -v https://api.example.com/events
```

Resolution: Verify the endpoint URL is correct and reachable from the pod. Check NetworkPolicies, egress rules, and DNS resolution. If the endpoint uses TLS, ensure the CA certificate is available at the path specified in `endpoint.tls.caFile`.

**Cause 2: Authentication failure**

The endpoint returns HTTP 401 or 403, which are non-retriable errors.

```bash
# Check for non-retriable failure metrics
curl -s http://localhost:8080/metrics | grep event_notification_non_retriable

# Check logs for payload dumps (logged at ERROR for non-retriable failures)
kubectl logs -n beacon -l app=beacon | grep -i "non-retriable"
```

Resolution: Verify the `ENDPOINT_AUTH_TOKEN` environment variable is set correctly in the Secret. Check the token is valid and has not expired.

**Cause 3: Endpoint returns 400 Bad Request**

The endpoint rejects the notification payload. This is a non-retriable failure; the record is flagged and no further attempts are made.

```bash
# Check for failed notifications in the database
kubectl logs -n beacon -l app=beacon | grep "non-retriable\|notification_failed"
```

Resolution: Examine the logged payload (logged at ERROR level) to understand why the endpoint rejected it. The payload format is documented in the architecture guide. If the endpoint requirements have changed, the notification payload structure may need updating.

**Cause 4: Request timeout**

The endpoint is reachable but takes too long to respond. Timeouts are treated as retriable errors.

```bash
kubectl logs -n beacon -l app=beacon | grep -i "timeout\|context deadline"
```

Resolution: Increase `endpoint.timeout` in the configuration if the endpoint legitimately requires more time.

---

## Database Locked

### Symptoms

- Logs show `database is locked` errors.
- Database operation metrics (`event_db_operation_errors_total`) increase.
- Notifications and state updates fail intermittently.

### Possible Causes and Resolutions

**Cause 1: Multiple instances accessing the same database file**

SQLite does not support concurrent access from multiple processes. If two pods mount the same PVC and access the same database file, lock contention occurs.

```bash
# Verify only one pod is running
kubectl get pods -n beacon -l app=beacon

# Verify deployment strategy is Recreate (not RollingUpdate)
kubectl get deployment beacon -n beacon -o jsonpath='{.spec.strategy.type}'
```

Resolution: Ensure `replicas: 1` and `strategy.type: Recreate` in the Deployment spec. Never run multiple replicas with a shared PVC.

**Cause 2: Stale WAL or SHM lock files**

After an unclean shutdown, SQLite may leave stale lock files (`.db-wal`, `.db-shm`) that prevent the database from opening cleanly.

```bash
# Check for lock files
kubectl exec -n beacon -l app=beacon -- ls -la /data/events.db*
```

Resolution: In most cases, SQLite automatically recovers WAL files on the next open. If the database remains locked:

1. Scale the deployment to 0.
2. Delete the WAL and SHM files (the WAL will be replayed):
   ```bash
   kubectl exec -n beacon -l app=beacon -- rm -f /data/events.db-wal /data/events.db-shm
   ```
3. Scale the deployment back to 1.

**Cause 3: Busy timeout too low**

Under high write load, the default busy timeout (5000ms) may not be sufficient.

Resolution: This is configured via a PRAGMA in the database code (`PRAGMA busy_timeout=5000`). If lock contention persists under normal load, the issue is likely Cause 1 (multiple writers).

---

## Watcher Disconnected

### Symptoms

- The `event_connection_status` metric drops to 0 for a resource type.
- The `event_reconnects_total` metric increases.
- New resource events are not being detected.
- Logs show watch-related errors or reconnection messages.

### Possible Causes and Resolutions

**Cause 1: API server temporarily unavailable**

The Kubernetes API server may be temporarily unavailable during cluster upgrades or maintenance.

```bash
# Check API server accessibility
kubectl get nodes
kubectl api-resources

# Check reconnection metrics
curl -s http://localhost:8080/metrics | grep event_reconnects_total
```

Resolution: Kubernetes informers automatically reconnect when the API server becomes available. Monitor the `event_connection_status` metric to confirm reconnection. The reconciliation loop will detect any events missed during the disconnection.

**Cause 2: RBAC permissions changed**

If the ClusterRole permissions are modified or the ClusterRoleBinding is deleted, the watcher loses access.

```bash
# Verify permissions
kubectl auth can-i watch pods --as=system:serviceaccount:beacon:beacon
kubectl auth can-i list pods --as=system:serviceaccount:beacon:beacon

# Check for RBAC errors in logs
kubectl logs -n beacon -l app=beacon | grep -i "forbidden\|unauthorized\|rbac"
```

Resolution: Re-apply the RBAC resources and restart the pod.

**Cause 3: CRD not installed**

If Beacon is configured to watch a custom resource type whose CRD is not installed in the cluster, the informer fails to start.

```bash
# Check if the CRD exists
kubectl get crd machines.maas.io

# Check for resource discovery errors
kubectl logs -n beacon -l app=beacon | grep -i "not found\|resource\|discovery"
```

Resolution: Install the CRD before deploying Beacon, or remove the resource type from the configuration.

---

## Storage Pressure

### Symptoms

- The `event_storage_pressure` metric with `severity=warning` or `severity=critical` is set to 1.
- The `event_storage_volume_usage_percent` metric exceeds the configured threshold.
- Pod logs show storage-related warnings.
- In extreme cases, the database fails to write and notifications stop processing.

### Possible Causes and Resolutions

**Cause 1: Database size growing due to high event volume**

A high volume of resource events accumulates records in the database faster than the cleanup job removes them.

```bash
# Check database size
curl -s http://localhost:8080/metrics | grep event_db_size_bytes

# Check volume usage
curl -s http://localhost:8080/metrics | grep event_storage_volume

# Check row counts
curl -s http://localhost:8080/metrics | grep event_db_rows_total
```

Resolution:
- Reduce the retention period (`retention.retentionPeriod`) to clean up records sooner.
- Increase the cleanup frequency (`retention.cleanupInterval`).
- Increase the PVC size if the current volume is too small for the event rate.
- Verify cleanup is enabled (`retention.enabled: true`).

**Cause 2: Failed notifications preventing cleanup**

Records with `notification_failed=true` are exempt from cleanup. If many notifications fail permanently, records accumulate.

```bash
# Check for failed notification counts
curl -s http://localhost:8080/metrics | grep event_notification_non_retriable

# Check the database for failed records
kubectl logs -n beacon -l app=beacon | grep "notification_failed\|non-retriable"
```

Resolution: Investigate and resolve the root cause of notification failures (see "Notifications Not Being Delivered" above). Once resolved, the failed records need to be manually removed from the database or their `notification_failed` flag reset.

**Cause 3: WAL file growing large**

Under sustained write load, the SQLite WAL file can grow large before being checkpointed back to the main database file.

```bash
# Check WAL file size
kubectl exec -n beacon -l app=beacon -- ls -la /data/events.db-wal
```

Resolution: SQLite automatically checkpoints the WAL file when it reaches a threshold. If the WAL file remains persistently large, restart the pod to force a checkpoint. The `PRAGMA incremental_vacuum` run by the cleanup job also helps reclaim space.

**Cause 4: Inode exhaustion**

On some filesystems, inode exhaustion can prevent new file creation even when disk space is available.

```bash
# Check inode metrics
curl -s http://localhost:8080/metrics | grep event_storage_volume_inodes
```

Resolution: This is unusual for Beacon since it uses a single database file. Check if other processes or sidecars are creating many small files on the same volume.

---

## General Debugging

### Checking Metrics

```bash
# Port-forward to the pod
kubectl port-forward -n beacon svc/beacon 8080:8080 &

# Query specific metric families
curl -s http://localhost:8080/metrics | grep event_endpoint
curl -s http://localhost:8080/metrics | grep event_notifications
curl -s http://localhost:8080/metrics | grep event_db_
curl -s http://localhost:8080/metrics | grep event_reconciliation
curl -s http://localhost:8080/metrics | grep event_cleanup
curl -s http://localhost:8080/metrics | grep event_storage
```

### Checking Logs

```bash
# All logs
kubectl logs -n beacon -l app=beacon

# Recent logs with timestamp
kubectl logs -n beacon -l app=beacon --since=5m

# Follow logs
kubectl logs -n beacon -l app=beacon -f

# Filter for errors (JSON log format)
kubectl logs -n beacon -l app=beacon | grep '"level":"error"'

# Filter for warnings
kubectl logs -n beacon -l app=beacon | grep '"level":"warn"'
```

### Checking Health Endpoints

```bash
# Liveness
curl -s http://localhost:8080/healthz | jq .

# Readiness (includes component check details)
curl -s http://localhost:8080/ready | jq .
```

### Inspecting the Database

If you need to query the SQLite database directly (for debugging only):

```bash
# Copy database to local machine
kubectl cp beacon/$(kubectl get pod -n beacon -l app=beacon -o jsonpath='{.items[0].metadata.name}'):/data/events.db ./events-debug.db

# Query with sqlite3
sqlite3 events-debug.db

# Useful queries:
# Count by state
SELECT cluster_state, COUNT(*) FROM managed_objects GROUP BY cluster_state;

# Pending notifications
SELECT id, resource_name, notified_created, notified_deleted, notification_failed
FROM managed_objects
WHERE (notified_created = 0 OR (cluster_state = 'deleted' AND notified_deleted = 0))
  AND notification_failed = 0;

# Failed notifications
SELECT id, resource_name, notification_failed_code, notification_attempts
FROM managed_objects
WHERE notification_failed = 1;

# Oldest records
SELECT id, resource_name, created_at, deleted_at
FROM managed_objects
ORDER BY created_at ASC
LIMIT 10;
```
