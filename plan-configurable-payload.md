# Plan: Configurable Annotations and Labels in Notification Payload

## Context

The beacon service sends notification payloads to an HTTP endpoint when annotated K8s objects are created/deleted. Currently it sends ALL labels but NO annotations (beyond the trigger annotation value). A metering system consuming these events needs specific annotations (e.g., customer-id, account-number) and specific labels included in the payload. This change adds a `payload` config section that lets users list which annotation and label keys to extract and include. The `payload` section is forward-compatible with a future fully-configurable payload.

**Decisions made:**
- Capture configured annotations at watcher detection time, store in a new DB column
- Annotations appear in `metadata.annotations` before `metadata.labels` in the payload
- Labels are filtered to configured keys; empty config = all labels (backward-compatible)
- Annotations are filtered to configured keys; empty config = no annotations

---

## Files to Modify

| # | File | Change |
|---|------|--------|
| 1 | `source/internal/config/config.go` | Add `PayloadConfig` struct and field |
| 2 | `source/internal/models/models.go` | Add `Annotations` to `ManagedObject` and `NotificationMetadata` |
| 3 | `source/internal/database/sqlite.go` | Add `annotations` column, migration, update all SQL/scan |
| 4 | `source/internal/database/mock.go` | No change needed (interface unchanged) |
| 5 | `source/internal/watcher/watcher.go` | Add `filterLabels`/`extractConfiguredAnnotations` helpers, update `extractFromPod` and `extractFromUnstructured` |
| 6 | `source/internal/reconciler/reconciler.go` | Same helpers, update `listPods` and `listDynamic` |
| 7 | `source/internal/notifier/notifier.go` | Update `buildPayload` to parse and include annotations |
| 8 | `source/deployments/configmap.yaml` | Add `payload` section example |
| 9 | `source/test-endpoint/internal/server/server.go` | Add `Annotations` to `EventMetadata` |
| 10 | `source/internal/config/testdata/valid_config.yaml` | Add `payload` section |
| 11 | `source/internal/watcher/watcher_test.go` | Tests for label filtering and annotation extraction |
| 12 | `source/internal/database/sqlite_test.go` | Update `newTestObject`, add round-trip test |
| 13 | `source/internal/notifier/notifier_test.go` | Test annotations in payload |

---

## Step 1: Config — Add PayloadConfig

**File:** `source/internal/config/config.go`

Add new struct:
```go
type PayloadConfig struct {
    Annotations []string `yaml:"annotations"` // Annotation keys to include in payload
    Labels      []string `yaml:"labels"`       // Label keys to include (empty = all)
}
```

Add field to `Config` struct (between `Annotation` and `Endpoint`):
```go
Payload        PayloadConfig        `yaml:"payload"`
```

No defaults or validation needed — empty slices are the zero value and give backward-compatible behavior.

---

## Step 2: Models — Add Annotations Fields

**File:** `source/internal/models/models.go`

Add to `ManagedObject` (after `Labels` field, line ~50):
```go
Annotations         string     `json:"annotations,omitempty"`
```

Update `NotificationMetadata` — add `Annotations` **before** `Labels`:
```go
type NotificationMetadata struct {
    Annotations     map[string]string `json:"annotations,omitempty"`
    Labels          map[string]string `json:"labels,omitempty"`
    ResourceVersion string            `json:"resourceVersion,omitempty"`
}
```

---

## Step 3: Database — Schema Migration + Updated Queries

**File:** `source/internal/database/sqlite.go`

**3a.** In `createSchema()` CREATE TABLE, add after `labels` line:
```sql
annotations TEXT NOT NULL DEFAULT '',
```

**3b.** Add `migrateSchema()` method called from `NewSQLiteDB` after `createSchema()`:
- Use `PRAGMA table_info(managed_objects)` to check if `annotations` column exists
- If missing, run `ALTER TABLE managed_objects ADD COLUMN annotations TEXT NOT NULL DEFAULT ''`
- Log migration

**3c.** Update all SQL column lists — add `annotations` between `labels` and `resource_version` in:
- `InsertManagedObject` — INSERT column list, placeholder, and `Exec` args
- `GetManagedObjectByUID` — SELECT list
- `GetManagedObjectByID` — SELECT list
- `GetPendingNotifications` — SELECT list
- `GetAllActiveObjects` — SELECT list
- `GetCleanupEligible` — SELECT list
- `scanManagedObject` — add `&obj.Annotations` to `Scan` after `&obj.Labels`
- `queryManagedObjects` — same `Scan` update

---

## Step 4: Watcher — Filter Labels and Extract Annotations

**File:** `source/internal/watcher/watcher.go`

**4a.** Add two helper methods:

```go
func (w *Watcher) filterLabels(allLabels map[string]string) map[string]string
```
- If `cfg.Payload.Labels` is empty, return allLabels unchanged
- Otherwise return a new map with only the configured keys

```go
func (w *Watcher) extractConfiguredAnnotations(allAnnotations map[string]string) map[string]string
```
- If `cfg.Payload.Annotations` is empty, return nil
- Otherwise return a new map with only the configured keys (skip missing keys)

**4b.** Update `extractFromPod` (line 374):
- Replace `json.Marshal(pod.Labels)` with `json.Marshal(w.filterLabels(pod.Labels))`
- Add `annotations := w.extractConfiguredAnnotations(pod.Annotations)` and marshal to JSON
- Set `Annotations: string(annotationsJSON)` on the returned ManagedObject

**4c.** Update `extractFromUnstructured` (line 405):
- Same pattern: filter labels, extract configured annotations, marshal, set on ManagedObject

---

## Step 5: Reconciler — Same Label/Annotation Extraction

**File:** `source/internal/reconciler/reconciler.go`

**5a.** Add the same `filterLabels` and `extractConfiguredAnnotations` methods on `Reconciler` (duplicated per existing codebase convention — both packages already duplicate `parseGVR`).

**5b.** Update `listPods` (line 261): Add `Labels` and `Annotations` fields to the `ManagedObject` being built. Requires adding `"encoding/json"` to imports.

**5c.** Update `listDynamic` (line 308): Same — add filtered labels and configured annotations.

---

## Step 6: Notifier — Include Annotations in Payload

**File:** `source/internal/notifier/notifier.go`

Update `buildPayload` (line 134). Add annotation parsing **before** the existing labels block:

```go
// Parse annotations JSON into map if present.
if obj.Annotations != "" {
    var annotations map[string]string
    if err := json.Unmarshal([]byte(obj.Annotations), &annotations); err == nil && len(annotations) > 0 {
        payload.Metadata.Annotations = annotations
    }
}

// Parse labels JSON into map if present. (existing code)
```

---

## Step 7: ConfigMap — Add Example payload Section

**File:** `source/deployments/configmap.yaml`

Add after the `annotation` block (after line 28):

```yaml
    payload:
      annotations:
        - example.com/customer-id
        - example.com/account
      labels:
        - app
        - version
```

---

## Step 8: Test Endpoint — Add Annotations to EventMetadata

**File:** `source/test-endpoint/internal/server/server.go`

Add `Annotations` field to `EventMetadata` before `Labels`:
```go
type EventMetadata struct {
    Annotations     map[string]string `json:"annotations,omitempty"`
    Labels          map[string]string `json:"labels,omitempty"`
    ResourceVersion string            `json:"resourceVersion,omitempty"`
}
```

---

## Step 9: Tests

**Config tests** (`source/internal/config/testdata/valid_config.yaml`, `source/internal/config/config_test.go`):
- Add `payload` section to valid_config.yaml
- Assert `cfg.Payload.Annotations` and `cfg.Payload.Labels` populated correctly
- Assert empty/nil when not configured (minimal_config.yaml)

**Watcher tests** (`source/internal/watcher/watcher_test.go`):
- `TestExtractManagedObject_Pod_FilteredLabels` — set `cfg.Payload.Labels = []string{"app"}`, verify only "app" in labels JSON
- `TestExtractManagedObject_Pod_NoLabelFilterSendsAll` — empty config, verify all labels present
- `TestExtractManagedObject_Pod_ExtractsConfiguredAnnotations` — set `cfg.Payload.Annotations = []string{"example.com/owner"}`, verify extracted
- `TestExtractManagedObject_Pod_NoAnnotationConfigEmpty` — empty config, verify annotations field is empty/null

**Database tests** (`source/internal/database/sqlite_test.go`):
- Update `newTestObject` to include `Annotations` field
- Add round-trip test verifying annotations survive insert/retrieve

**Notifier tests** (`source/internal/notifier/notifier_test.go`):
- Test `buildPayload` includes annotations in metadata when present
- Test `buildPayload` omits annotations when empty

---

## Resulting Payload Shape

```json
{
  "id": "550e8400-...",
  "timestamp": "2026-02-14T10:30:00Z",
  "eventType": "created",
  "resource": {
    "uid": "k8s-uid-123",
    "type": "LLMInferenceService",
    "name": "my-service",
    "namespace": "default",
    "annotationValue": "true"
  },
  "metadata": {
    "annotations": {
      "example.com/customer-id": "C-12345",
      "example.com/account": "A-67890"
    },
    "labels": {
      "app": "my-service",
      "version": "1.0.0"
    },
    "resourceVersion": "789"
  }
}
```

---

## Backward Compatibility

| Config state | Behavior |
|---|---|
| No `payload` section | All labels sent, no annotations — identical to current behavior |
| `payload.labels` empty | All labels sent |
| `payload.labels` populated | Only listed label keys sent |
| `payload.annotations` empty | No annotations in payload |
| `payload.annotations` populated | Only listed annotation keys sent |

---

## Verification

1. `cd source && go test ./...` — all unit tests pass
2. Build: `cd source && make build`
3. Build image: `cd source && make image-build`
4. Deploy with ConfigMap containing `payload.annotations` and `payload.labels`
5. Create an annotated LLMInferenceService or Pod with the configured annotation/label keys
6. Check test-endpoint logs: verify `metadata.annotations` appears with correct keys, `metadata.labels` shows only configured keys
7. Check test-endpoint logs with no `payload` config: verify all labels sent, no annotations — backward compatible
