// Package reconciler implements the periodic reconciliation loop that detects
// drift between the cluster state and the database. It lists annotated objects
// from the Kubernetes API and compares them against the database records,
// inserting missed creations and marking missed deletions.
package reconciler

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"

	"github.com/bryonbaker/beacon/internal/config"
	"github.com/bryonbaker/beacon/internal/database"
	"github.com/bryonbaker/beacon/internal/metrics"
	"github.com/bryonbaker/beacon/internal/models"
)

// Reconciler periodically compares the cluster state with the database to
// detect missed creation and deletion events.
type Reconciler struct {
	db          database.Database
	typedClient kubernetes.Interface
	dynClient   dynamic.Interface
	cfg         *config.Config
	metrics     *metrics.Metrics
	logger      *zap.Logger
}

// NewReconciler creates a new Reconciler with the provided dependencies.
func NewReconciler(
	db database.Database,
	typedClient kubernetes.Interface,
	dynClient dynamic.Interface,
	cfg *config.Config,
	m *metrics.Metrics,
	logger *zap.Logger,
) *Reconciler {
	return &Reconciler{
		db:          db,
		typedClient: typedClient,
		dynClient:   dynClient,
		cfg:         cfg,
		metrics:     m,
		logger:      logger,
	}
}

// Start begins the reconciliation loop. If cfg.Reconciliation.OnStartup is
// true, an initial reconciliation is performed immediately. Subsequent
// reconciliations are triggered at the configured interval. The loop stops
// when ctx is cancelled.
func (r *Reconciler) Start(ctx context.Context) {
	r.logger.Info("reconciler started",
		zap.Duration("interval", r.cfg.Reconciliation.Interval.Duration),
		zap.Bool("on_startup", r.cfg.Reconciliation.OnStartup),
	)

	if r.cfg.Reconciliation.OnStartup {
		if err := r.Reconcile(ctx); err != nil {
			r.logger.Error("startup reconciliation failed", zap.Error(err))
		}
	}

	ticker := time.NewTicker(r.cfg.Reconciliation.Interval.Duration)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			r.logger.Info("reconciler stopping", zap.Error(ctx.Err()))
			return
		case <-ticker.C:
			if err := r.Reconcile(ctx); err != nil {
				r.logger.Error("reconciliation failed", zap.Error(err))
			}
		}
	}
}

// Reconcile performs a single reconciliation pass over all configured
// resources. For each resource type it lists annotated objects from the
// cluster, retrieves the corresponding database records, and diffs by UID.
//
// Objects present in the cluster but absent from the database are treated as
// missed creations and inserted. Objects present in the database but absent
// from the cluster are treated as missed deletions and marked as deleted.
func (r *Reconciler) Reconcile(ctx context.Context) error {
	start := time.Now()
	r.logger.Info("reconciliation started")

	var reconcileErr error
	for _, res := range r.cfg.Resources {
		resourceType := res.Kind

		if err := r.reconcileResource(ctx, res, resourceType); err != nil {
			r.logger.Error("failed to reconcile resource type",
				zap.String("resource_type", resourceType),
				zap.Error(err),
			)
			reconcileErr = err
		}
	}

	duration := time.Since(start)
	r.metrics.ReconciliationDuration.Observe(duration.Seconds())

	if reconcileErr != nil {
		r.metrics.ReconciliationRunsTotal.WithLabelValues("error").Inc()
		return fmt.Errorf("reconciliation completed with errors: %w", reconcileErr)
	}

	r.metrics.ReconciliationRunsTotal.WithLabelValues("success").Inc()
	r.logger.Info("reconciliation completed",
		zap.Duration("duration", duration),
	)
	return nil
}

// reconcileResource performs the diff for a single resource type.
func (r *Reconciler) reconcileResource(ctx context.Context, res config.ResourceConfig, resourceType string) error {
	// List annotated objects from the cluster.
	clusterUIDs, clusterObjects, err := r.listClusterObjects(ctx, res)
	if err != nil {
		return fmt.Errorf("listing cluster objects for %s: %w", resourceType, err)
	}

	// Get all active objects from the database for this resource type.
	dbObjects, err := r.db.GetAllActiveObjects(resourceType)
	if err != nil {
		return fmt.Errorf("querying active objects for %s: %w", resourceType, err)
	}

	// Build a map of DB UIDs for efficient lookup.
	dbUIDMap := make(map[string]*models.ManagedObject, len(dbObjects))
	for _, obj := range dbObjects {
		dbUIDMap[obj.ResourceUID] = obj
	}

	// Detect missed creations: objects in the cluster but not in the DB.
	missedCreations := 0
	for uid, clusterObj := range clusterObjects {
		if _, exists := dbUIDMap[uid]; !exists {
			r.logger.Warn("missed creation detected during reconciliation",
				zap.String("resource_type", resourceType),
				zap.String("resource_uid", uid),
				zap.String("resource_name", clusterObj.ResourceName),
				zap.String("namespace", clusterObj.ResourceNamespace),
			)

			clusterObj.DetectionSource = models.DetectionSourceReconciliation
			clusterObj.ClusterState = models.ClusterStateExists

			if err := r.db.InsertManagedObject(clusterObj); err != nil {
				r.logger.Error("failed to insert missed object",
					zap.String("resource_uid", uid),
					zap.Error(err),
				)
				continue
			}

			missedCreations++
			r.metrics.ReconciliationDriftDetected.WithLabelValues(resourceType, "missed_creation").Inc()
			r.metrics.ReconciliationObjectsProcessed.WithLabelValues(resourceType, "insert").Inc()
		}
	}

	// Detect missed deletions: objects in the DB but not in the cluster.
	missedDeletions := 0
	now := time.Now()
	for uid, dbObj := range dbUIDMap {
		if _, exists := clusterUIDs[uid]; !exists {
			r.logger.Warn("missed deletion detected during reconciliation",
				zap.String("resource_type", resourceType),
				zap.String("resource_uid", uid),
				zap.String("resource_name", dbObj.ResourceName),
				zap.String("namespace", dbObj.ResourceNamespace),
			)

			if err := r.db.UpdateClusterState(uid, models.ClusterStateDeleted, &now); err != nil {
				r.logger.Error("failed to mark missed deletion",
					zap.String("resource_uid", uid),
					zap.Error(err),
				)
				continue
			}

			missedDeletions++
			r.metrics.ReconciliationDriftDetected.WithLabelValues(resourceType, "missed_deletion").Inc()
			r.metrics.ReconciliationObjectsProcessed.WithLabelValues(resourceType, "delete").Inc()
		}
	}

	// Update last_reconciled for all DB objects that are still present in the cluster.
	reconciledAt := time.Now()
	for uid, dbObj := range dbUIDMap {
		if _, exists := clusterUIDs[uid]; exists {
			if err := r.db.UpdateLastReconciled(dbObj.ID, reconciledAt); err != nil {
				r.logger.Error("failed to update last_reconciled",
					zap.String("resource_uid", uid),
					zap.Error(err),
				)
			}
			r.metrics.ReconciliationObjectsProcessed.WithLabelValues(resourceType, "reconciled").Inc()
		}
	}

	r.logger.Info("resource reconciliation complete",
		zap.String("resource_type", resourceType),
		zap.Int("cluster_objects", len(clusterUIDs)),
		zap.Int("db_objects", len(dbObjects)),
		zap.Int("missed_creations", missedCreations),
		zap.Int("missed_deletions", missedDeletions),
	)

	return nil
}

// listClusterObjects queries the Kubernetes API for annotated objects of the
// given resource type. It returns a set of UIDs and a map of UID to
// ManagedObject for objects that carry the configured annotation.
func (r *Reconciler) listClusterObjects(ctx context.Context, res config.ResourceConfig) (map[string]struct{}, map[string]*models.ManagedObject, error) {
	if res.APIVersion == "v1" && res.Kind == "Pod" {
		return r.listPods(ctx, res)
	}
	return r.listDynamic(ctx, res)
}

// listPods lists Pods using the typed client and filters by annotation.
func (r *Reconciler) listPods(ctx context.Context, res config.ResourceConfig) (map[string]struct{}, map[string]*models.ManagedObject, error) {
	uidSet := make(map[string]struct{})
	objMap := make(map[string]*models.ManagedObject)

	namespaces := res.Namespaces
	if len(namespaces) == 0 {
		namespaces = []string{""}
	}

	for _, ns := range namespaces {
		podList, err := r.typedClient.CoreV1().Pods(ns).List(ctx, metav1.ListOptions{})
		if err != nil {
			return nil, nil, fmt.Errorf("listing pods in namespace %q: %w", ns, err)
		}

		for i := range podList.Items {
			pod := &podList.Items[i]
			annotationValue, hasAnnotation := r.getAnnotation(pod.Annotations)
			if !hasAnnotation {
				continue
			}

			uid := string(pod.UID)
			uidSet[uid] = struct{}{}
			objMap[uid] = &models.ManagedObject{
				ID:                uuid.New().String(),
				ResourceUID:       uid,
				ResourceType:      res.Kind,
				ResourceName:      pod.Name,
				ResourceNamespace: pod.Namespace,
				AnnotationValue:   annotationValue,
				ResourceVersion:   pod.ResourceVersion,
				CreatedAt:         time.Now(),
			}
		}
	}

	return uidSet, objMap, nil
}

// listDynamic lists custom resources using the dynamic client and filters by annotation.
func (r *Reconciler) listDynamic(ctx context.Context, res config.ResourceConfig) (map[string]struct{}, map[string]*models.ManagedObject, error) {
	gvr, err := parseGVR(res.APIVersion, res.Kind, res.Resource)
	if err != nil {
		return nil, nil, fmt.Errorf("parsing GVR for %s/%s: %w", res.APIVersion, res.Kind, err)
	}

	uidSet := make(map[string]struct{})
	objMap := make(map[string]*models.ManagedObject)

	namespaces := res.Namespaces
	if len(namespaces) == 0 {
		namespaces = []string{""}
	}

	for _, ns := range namespaces {
		list, err := r.dynClient.Resource(gvr).Namespace(ns).List(ctx, metav1.ListOptions{})
		if err != nil {
			return nil, nil, fmt.Errorf("listing %s in namespace %q: %w", res.Kind, ns, err)
		}

		for i := range list.Items {
			item := &list.Items[i]
			annotations := item.GetAnnotations()
			annotationValue, hasAnnotation := r.getAnnotation(annotations)
			if !hasAnnotation {
				continue
			}

			uid := string(item.GetUID())
			uidSet[uid] = struct{}{}
			objMap[uid] = &models.ManagedObject{
				ID:                uuid.New().String(),
				ResourceUID:       uid,
				ResourceType:      res.Kind,
				ResourceName:      item.GetName(),
				ResourceNamespace: item.GetNamespace(),
				AnnotationValue:   annotationValue,
				ResourceVersion:   item.GetResourceVersion(),
				CreatedAt:         time.Now(),
			}
		}
	}

	return uidSet, objMap, nil
}

// getAnnotation checks whether the given annotations map contains the
// configured annotation key. It returns the value and a boolean indicating
// presence.
func (r *Reconciler) getAnnotation(annotations map[string]string) (string, bool) {
	if annotations == nil {
		return "", false
	}
	val, ok := annotations[r.cfg.Annotation.Key]
	return val, ok
}

// parseGVR parses an apiVersion, kind, and optional resource name into a
// GroupVersionResource. If resource is provided it is used as-is; otherwise
// the kind is lowercased with an "s" suffix as a fallback heuristic.
func parseGVR(apiVersion, kind, resource string) (schema.GroupVersionResource, error) {
	var group, version string
	parts := strings.SplitN(apiVersion, "/", 2)
	if len(parts) == 1 {
		group = ""
		version = parts[0]
	} else {
		group = parts[0]
		version = parts[1]
	}

	if resource == "" {
		resource = strings.ToLower(kind) + "s"
	}

	return schema.GroupVersionResource{
		Group:    group,
		Version:  version,
		Resource: resource,
	}, nil
}

