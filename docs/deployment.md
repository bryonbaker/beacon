# Beacon Deployment Guide

## Prerequisites

- A running Kubernetes 1.24+ or OpenShift 4.12+ cluster
- `kubectl` (or `oc`) configured to access the cluster
- Podman for building the container image
- Go 1.21+ with CGO support (for building from source)
- Access to `quay.io/bryonbaker/beacon` (or your own registry)
- A PersistentVolumeClaim-capable StorageClass

## Build and Release

All build operations use the Makefile in `source/`. Run everything from the `source/` directory:

```bash
cd source/
```

### Quick Reference

```bash
make help          # Show all available targets
make build         # Build binary for current platform
make test          # Run unit tests with race detection
make image-build   # Build container image with Podman
make image-push    # Push to registry
make release       # Run CI checks, build, and push (full pipeline)
```

### Building the Binary

```bash
# Build for current platform
make build

# Build for Linux (for container use)
make build-linux
```

The binary is compiled with `CGO_ENABLED=1` (required for SQLite) and linker flags `-s -w` to strip debug symbols.

### Running Tests

```bash
# Unit tests with race detection
make test

# Tests with coverage report (generates coverage.html)
make test-coverage

# Full CI checks (deps, fmt, vet, lint, test)
make ci
```

### Building the Container Image

The Makefile defaults to pushing to `quay.io/bryonbaker/beacon`. Both a version tag and `latest` are applied.

```bash
# Build the image (tags as both :version and :latest)
make image-build

# Push to quay.io
make image-push

# Build and push in one command
make image-build-push
```

The image is tagged as:
- `quay.io/bryonbaker/beacon:<version>` (from `git describe --tags --always`)
- `quay.io/bryonbaker/beacon:latest`

To override the registry or repository:

```bash
make image-build-push REGISTRY=myregistry.io REPOSITORY=myorg IMAGE_NAME=beacon
```

### Full Release

The `release` target runs the complete pipeline: dependencies, formatting, vetting, linting, tests, then builds and pushes the image.

```bash
make release
```

## Deploying to Kubernetes

### Using the Makefile (Recommended)

All Kubernetes manifests are in `source/deployments/`. The Makefile provides targets to apply them:

```bash
# Deploy all manifests (namespace, RBAC, configmap, secret, PVC, deployment, service, servicemonitor)
make deploy-dev

# Deploy to production (prompts for confirmation)
make deploy-prod

# Remove all deployed resources
make undeploy
```

### Before Deploying

1. **Update the secret** with your actual endpoint auth token:

   Edit `deployments/secret.yaml` and replace the placeholder:

   ```yaml
   stringData:
     auth-token: "YOUR_ACTUAL_TOKEN_HERE"
   ```

2. **Update the configmap** with your endpoint URL and resources to watch:

   Edit `deployments/configmap.yaml` and set `endpoint.url` and the `resources` list.

3. **Update the deployment image** to match your registry:

   Edit `deployments/deployment.yaml` and set the `image` field:

   ```yaml
   image: quay.io/bryonbaker/beacon:latest
   ```

### Step-by-Step Manual Deployment

If you prefer to apply manifests individually:

```bash
cd source/

# 1. Create namespace
kubectl apply -f deployments/namespace.yaml

# 2. Create RBAC resources
kubectl apply -f deployments/serviceaccount.yaml
kubectl apply -f deployments/clusterrole.yaml
kubectl apply -f deployments/clusterrolebinding.yaml

# 3. Create config and secrets
kubectl apply -f deployments/configmap.yaml
kubectl apply -f deployments/secret.yaml

# 4. Create persistent storage
kubectl apply -f deployments/pvc.yaml

# 5. Deploy the application
kubectl apply -f deployments/deployment.yaml
kubectl apply -f deployments/service.yaml

# 6. (Optional) Prometheus monitoring
kubectl apply -f deployments/servicemonitor.yaml
kubectl apply -f deployments/prometheusrule.yaml
```

### Verify the Deployment

```bash
# Check pod status
kubectl get pods -n beacon -l app=beacon

# Check logs
make logs
# or: kubectl logs -f -n beacon -l app=beacon

# Port-forward and check endpoints
make port-forward-metrics
# then in another terminal:
curl http://localhost:8080/healthz
curl http://localhost:8080/ready
curl -s http://localhost:8080/metrics | head -30
```

### Operational Commands

```bash
# Tail application logs
make logs

# Port-forward metrics to localhost:8080
make port-forward-metrics

# Open a shell in the pod
make db-shell

# Run a SQL query against the database
make db-query SQL="SELECT COUNT(*) FROM managed_objects"
make db-query SQL="SELECT cluster_state, COUNT(*) FROM managed_objects GROUP BY cluster_state"
```

## Upgrade Guide

### Configuration Changes

Configuration changes require a pod restart (Beacon reads config once at startup):

```bash
# Edit the configmap
kubectl edit configmap beacon-config -n beacon

# Restart the pod
kubectl rollout restart deployment beacon -n beacon

# Verify
kubectl rollout status deployment beacon -n beacon
```

### Image Upgrades

```bash
# Build and push the new version
cd source/
make release

# Update the deployment
kubectl set image deployment/beacon \
  beacon=quay.io/bryonbaker/beacon:latest \
  -n beacon

# Monitor the rollout
kubectl rollout status deployment beacon -n beacon
```

### Data Safety During Upgrades

The `Recreate` deployment strategy ensures the old pod terminates before the new pod starts, preventing concurrent SQLite access. The PVC preserves all data across restarts. No data migration is needed between versions — the schema uses `IF NOT EXISTS` clauses.

## Backup and Restore

### Backing Up the SQLite Database

**Method 1: Copy from the running pod**

```bash
POD=$(kubectl get pod -n beacon -l app=beacon -o jsonpath='{.items[0].metadata.name}')
kubectl cp beacon/$POD:/data/events.db ./events-backup.db
kubectl cp beacon/$POD:/data/events.db-wal ./events-backup.db-wal 2>/dev/null || true
```

**Method 2: Volume snapshot (recommended for production)**

```yaml
apiVersion: snapshot.storage.k8s.io/v1
kind: VolumeSnapshot
metadata:
  name: beacon-data-backup
  namespace: beacon
spec:
  source:
    persistentVolumeClaimName: beacon-data
```

**Method 3: Stop and copy**

```bash
kubectl scale deployment beacon -n beacon --replicas=0
# ... copy from PVC using a temporary pod ...
kubectl scale deployment beacon -n beacon --replicas=1
```

### Restoring

1. Scale down: `kubectl scale deployment beacon -n beacon --replicas=0`
2. Copy backup into the PVC via a temporary pod
3. Scale up: `kubectl scale deployment beacon -n beacon --replicas=1`
4. Verify: `make logs` and `curl http://localhost:8080/ready`

### Backup Schedule Recommendations

| Environment | Frequency | Retention | Method |
|---|---|---|---|
| Development | On demand | 1 copy | kubectl cp |
| Staging | Daily | 7 days | Volume snapshot |
| Production | Every 6 hours | 30 days | Volume snapshot with lifecycle policy |
