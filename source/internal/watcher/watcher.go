// Package watcher provides Kubernetes resource watching capabilities for the
// beacon service. It sets up informers for configured resource types
// and reacts to add, update, and delete events.
package watcher

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/dynamic/dynamicinformer"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"

	"github.com/bryonbaker/beacon/internal/config"
	"github.com/bryonbaker/beacon/internal/database"
	"github.com/bryonbaker/beacon/internal/metrics"
	"github.com/bryonbaker/beacon/internal/models"
)

// Watcher monitors Kubernetes resources for annotation changes and persists
// tracked objects to the database.
type Watcher struct {
	db          database.Database
	typedClient kubernetes.Interface
	dynClient   dynamic.Interface
	cfg         *config.Config
	metrics     *metrics.Metrics
	logger      *zap.Logger
	stopChs     []chan struct{}
}

// NewWatcher creates a new Watcher with the provided dependencies.
func NewWatcher(
	db database.Database,
	typedClient kubernetes.Interface,
	dynClient dynamic.Interface,
	cfg *config.Config,
	m *metrics.Metrics,
	logger *zap.Logger,
) *Watcher {
	return &Watcher{
		db:          db,
		typedClient: typedClient,
		dynClient:   dynClient,
		cfg:         cfg,
		metrics:     m,
		logger:      logger,
	}
}

// Start begins watching all configured resources. It creates informers for each
// resource type and registers event handlers. For core API resources (apiVersion
// "v1", kind "Pod") it uses a typed informer; for all other resources it uses a
// dynamic informer.
func (w *Watcher) Start(ctx context.Context) error {
	for _, res := range w.cfg.Resources {
		stopCh := make(chan struct{})
		w.stopChs = append(w.stopChs, stopCh)

		resourceType := res.Kind

		if res.APIVersion == "v1" && res.Kind == "Pod" {
			if err := w.startTypedPodInformer(ctx, res, resourceType, stopCh); err != nil {
				return fmt.Errorf("starting typed informer for %s: %w", resourceType, err)
			}
		} else {
			if err := w.startDynamicInformer(ctx, res, resourceType, stopCh); err != nil {
				return fmt.Errorf("starting dynamic informer for %s: %w", resourceType, err)
			}
		}

		w.logger.Info("started watching resource",
			zap.String("apiVersion", res.APIVersion),
			zap.String("kind", res.Kind),
			zap.Strings("namespaces", res.Namespaces),
		)
	}
	return nil
}

// Stop closes all informer stop channels, causing the informers to shut down.
func (w *Watcher) Stop() {
	for _, ch := range w.stopChs {
		close(ch)
	}
	w.logger.Info("all watchers stopped")
}

// startTypedPodInformer creates a typed informer for Pod resources using the
// shared informer factory.
func (w *Watcher) startTypedPodInformer(_ context.Context, res config.ResourceConfig, resourceType string, stopCh chan struct{}) error {
	if len(res.Namespaces) > 0 {
		// Use the first namespace; for multiple namespaces, create one informer each.
		for _, ns := range res.Namespaces {
			nsStopCh := make(chan struct{})
			w.stopChs = append(w.stopChs, nsStopCh)

			factory := informers.NewSharedInformerFactoryWithOptions(
				w.typedClient,
				0,
				informers.WithNamespace(ns),
			)

			informer := factory.Core().V1().Pods().Informer()
			informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
				AddFunc: func(obj interface{}) {
					w.handleAdd(obj, resourceType, models.DetectionSourceWatch)
				},
				UpdateFunc: func(oldObj, newObj interface{}) {
					w.handleUpdate(oldObj, newObj, resourceType)
				},
				DeleteFunc: func(obj interface{}) {
					w.handleDelete(obj, resourceType)
				},
			})

			go factory.Start(nsStopCh)
		}
		return nil
	}

	// Watch all namespaces.
	factory := informers.NewSharedInformerFactory(w.typedClient, 0)
	informer := factory.Core().V1().Pods().Informer()
	informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			w.handleAdd(obj, resourceType, models.DetectionSourceWatch)
		},
		UpdateFunc: func(oldObj, newObj interface{}) {
			w.handleUpdate(oldObj, newObj, resourceType)
		},
		DeleteFunc: func(obj interface{}) {
			w.handleDelete(obj, resourceType)
		},
	})

	go factory.Start(stopCh)
	return nil
}

// startDynamicInformer creates a dynamic informer for custom resources.
func (w *Watcher) startDynamicInformer(_ context.Context, res config.ResourceConfig, resourceType string, stopCh chan struct{}) error {
	gvr, err := parseGVR(res.APIVersion, res.Kind, res.Resource)
	if err != nil {
		return fmt.Errorf("parsing GVR for %s/%s: %w", res.APIVersion, res.Kind, err)
	}

	if len(res.Namespaces) > 0 {
		for _, ns := range res.Namespaces {
			nsStopCh := make(chan struct{})
			w.stopChs = append(w.stopChs, nsStopCh)

			factory := dynamicinformer.NewFilteredDynamicSharedInformerFactory(
				w.dynClient,
				0,
				ns,
				nil,
			)

			informer := factory.ForResource(gvr).Informer()
			informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
				AddFunc: func(obj interface{}) {
					w.handleAdd(obj, resourceType, models.DetectionSourceWatch)
				},
				UpdateFunc: func(oldObj, newObj interface{}) {
					w.handleUpdate(oldObj, newObj, resourceType)
				},
				DeleteFunc: func(obj interface{}) {
					w.handleDelete(obj, resourceType)
				},
			})

			go factory.Start(nsStopCh)
		}
		return nil
	}

	// Watch all namespaces.
	factory := dynamicinformer.NewDynamicSharedInformerFactory(w.dynClient, 0)
	informer := factory.ForResource(gvr).Informer()
	informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			w.handleAdd(obj, resourceType, models.DetectionSourceWatch)
		},
		UpdateFunc: func(oldObj, newObj interface{}) {
			w.handleUpdate(oldObj, newObj, resourceType)
		},
		DeleteFunc: func(obj interface{}) {
			w.handleDelete(obj, resourceType)
		},
	})

	go factory.Start(stopCh)
	return nil
}

// handleAdd processes a newly observed resource. If the resource carries the
// configured annotation, it is inserted into the database.
func (w *Watcher) handleAdd(obj interface{}, resourceType string, detectionSource string) {
	mo, err := w.extractManagedObject(obj, resourceType)
	if err != nil {
		w.logger.Error("failed to extract managed object on add",
			zap.String("resource_type", resourceType),
			zap.Error(err),
		)
		return
	}

	annotated, annotationValue := w.hasAnnotation(obj, w.cfg.Annotation.Key)
	if !annotated {
		return
	}

	mo.AnnotationValue = annotationValue
	mo.DetectionSource = detectionSource
	mo.ClusterState = models.ClusterStateExists

	if err := w.db.InsertManagedObject(mo); err != nil {
		w.logger.Error("failed to insert managed object",
			zap.String("resource_uid", mo.ResourceUID),
			zap.String("resource_name", mo.ResourceName),
			zap.Error(err),
		)
		return
	}

	w.metrics.RecordResourceEvent(resourceType, "add")
	w.logger.Info("tracked new annotated resource",
		zap.String("resource_uid", mo.ResourceUID),
		zap.String("resource_name", mo.ResourceName),
		zap.String("namespace", mo.ResourceNamespace),
		zap.String("resource_type", resourceType),
		zap.String("detection_source", detectionSource),
		zap.String("annotation_value", annotationValue),
	)
}

// handleUpdate processes resource updates. It detects annotation mutations:
//   - Annotation added (old does not have it, new does): treated as a creation
//     event with detection_source "mutation".
//   - Annotation removed (old has it, new does not): treated as a logical
//     deletion with detection_source "mutation".
func (w *Watcher) handleUpdate(oldObj, newObj interface{}, resourceType string) {
	oldAnnotated, _ := w.hasAnnotation(oldObj, w.cfg.Annotation.Key)
	newAnnotated, newAnnotationValue := w.hasAnnotation(newObj, w.cfg.Annotation.Key)

	switch {
	case !oldAnnotated && newAnnotated:
		// Annotation was added via mutation.
		w.logger.Warn("annotation added via mutation",
			zap.String("resource_type", resourceType),
			zap.String("annotation_value", newAnnotationValue),
		)
		w.metrics.RecordAnnotationMutation(resourceType, "added")
		w.handleAdd(newObj, resourceType, models.DetectionSourceMutation)

	case oldAnnotated && !newAnnotated:
		// Annotation was removed via mutation.
		mo, err := w.extractManagedObject(oldObj, resourceType)
		if err != nil {
			w.logger.Error("failed to extract managed object on annotation removal",
				zap.String("resource_type", resourceType),
				zap.Error(err),
			)
			return
		}

		w.logger.Warn("annotation removed via mutation",
			zap.String("resource_type", resourceType),
			zap.String("resource_uid", mo.ResourceUID),
			zap.String("resource_name", mo.ResourceName),
		)

		now := time.Now()
		if err := w.db.UpdateClusterState(mo.ResourceUID, models.ClusterStateDeleted, &now); err != nil {
			w.logger.Error("failed to update cluster state on annotation removal",
				zap.String("resource_uid", mo.ResourceUID),
				zap.Error(err),
			)
			return
		}

		w.metrics.RecordAnnotationMutation(resourceType, "removed")
		w.metrics.RecordResourceEvent(resourceType, "delete")

	case oldAnnotated && newAnnotated:
		// Both have the annotation; annotation value may have changed.
		w.metrics.RecordResourceEvent(resourceType, "update")
	}
}

// handleDelete processes resource deletion events. If the resource was being
// tracked (found in the database by UID), its cluster state is set to deleted.
func (w *Watcher) handleDelete(obj interface{}, resourceType string) {
	// Handle DeletedFinalStateUnknown tombstones.
	if d, ok := obj.(cache.DeletedFinalStateUnknown); ok {
		obj = d.Obj
	}

	mo, err := w.extractManagedObject(obj, resourceType)
	if err != nil {
		w.logger.Error("failed to extract managed object on delete",
			zap.String("resource_type", resourceType),
			zap.Error(err),
		)
		return
	}

	// Check if this object is tracked in the database.
	existing, err := w.db.GetManagedObjectByUID(mo.ResourceUID)
	if err != nil {
		w.logger.Debug("deleted resource not found in database, ignoring",
			zap.String("resource_uid", mo.ResourceUID),
			zap.String("resource_type", resourceType),
		)
		return
	}

	if existing == nil {
		return
	}

	now := time.Now()
	if err := w.db.UpdateClusterState(mo.ResourceUID, models.ClusterStateDeleted, &now); err != nil {
		w.logger.Error("failed to update cluster state on delete",
			zap.String("resource_uid", mo.ResourceUID),
			zap.Error(err),
		)
		return
	}

	w.metrics.RecordResourceEvent(resourceType, "delete")
	w.logger.Info("tracked resource deleted",
		zap.String("resource_uid", mo.ResourceUID),
		zap.String("resource_name", mo.ResourceName),
		zap.String("namespace", mo.ResourceNamespace),
		zap.String("resource_type", resourceType),
	)
}

// extractManagedObject builds a ManagedObject from a Kubernetes runtime object.
// It handles typed *corev1.Pod objects and *unstructured.Unstructured objects
// (used for custom resources).
func (w *Watcher) extractManagedObject(obj interface{}, resourceType string) (*models.ManagedObject, error) {
	switch o := obj.(type) {
	case *corev1.Pod:
		return w.extractFromPod(o, resourceType)
	case *unstructured.Unstructured:
		return w.extractFromUnstructured(o, resourceType)
	default:
		// Attempt to convert via runtime if possible.
		u, err := runtime.DefaultUnstructuredConverter.ToUnstructured(obj)
		if err != nil {
			return nil, fmt.Errorf("unsupported object type %T: %w", obj, err)
		}
		uns := &unstructured.Unstructured{Object: u}
		return w.extractFromUnstructured(uns, resourceType)
	}
}

// extractFromPod extracts a ManagedObject from a typed Pod.
func (w *Watcher) extractFromPod(pod *corev1.Pod, resourceType string) (*models.ManagedObject, error) {
	labelsJSON, err := json.Marshal(pod.Labels)
	if err != nil {
		labelsJSON = []byte("{}")
	}

	metadataJSON, err := json.Marshal(pod.ObjectMeta)
	if err != nil {
		metadataJSON = []byte("{}")
	}

	annotationValue := ""
	if pod.Annotations != nil {
		annotationValue = pod.Annotations[w.cfg.Annotation.Key]
	}

	return &models.ManagedObject{
		ID:                uuid.New().String(),
		ResourceUID:       string(pod.UID),
		ResourceType:      resourceType,
		ResourceName:      pod.Name,
		ResourceNamespace: pod.Namespace,
		AnnotationValue:   annotationValue,
		ResourceVersion:   pod.ResourceVersion,
		Labels:            string(labelsJSON),
		FullMetadata:      string(metadataJSON),
		CreatedAt:         time.Now(),
	}, nil
}

// extractFromUnstructured extracts a ManagedObject from an unstructured object.
func (w *Watcher) extractFromUnstructured(obj *unstructured.Unstructured, resourceType string) (*models.ManagedObject, error) {
	labelsJSON, err := json.Marshal(obj.GetLabels())
	if err != nil {
		labelsJSON = []byte("{}")
	}

	metadata := map[string]interface{}{
		"name":            obj.GetName(),
		"namespace":       obj.GetNamespace(),
		"uid":             obj.GetUID(),
		"resourceVersion": obj.GetResourceVersion(),
		"labels":          obj.GetLabels(),
		"annotations":     obj.GetAnnotations(),
	}
	metadataJSON, err := json.Marshal(metadata)
	if err != nil {
		metadataJSON = []byte("{}")
	}

	annotationValue := ""
	annotations := obj.GetAnnotations()
	if annotations != nil {
		annotationValue = annotations[w.cfg.Annotation.Key]
	}

	return &models.ManagedObject{
		ID:                uuid.New().String(),
		ResourceUID:       string(obj.GetUID()),
		ResourceType:      resourceType,
		ResourceName:      obj.GetName(),
		ResourceNamespace: obj.GetNamespace(),
		AnnotationValue:   annotationValue,
		ResourceVersion:   obj.GetResourceVersion(),
		Labels:            string(labelsJSON),
		FullMetadata:      string(metadataJSON),
		CreatedAt:         time.Now(),
	}, nil
}

// hasAnnotation checks whether a Kubernetes object carries the specified
// annotation key. It returns true along with the annotation value if found.
func (w *Watcher) hasAnnotation(obj interface{}, annotationKey string) (bool, string) {
	switch o := obj.(type) {
	case *corev1.Pod:
		if o.Annotations == nil {
			return false, ""
		}
		val, ok := o.Annotations[annotationKey]
		return ok, val
	case *unstructured.Unstructured:
		annotations := o.GetAnnotations()
		if annotations == nil {
			return false, ""
		}
		val, ok := annotations[annotationKey]
		return ok, val
	default:
		// Best-effort: try to convert to metav1.ObjectMeta accessor.
		if accessor, ok := obj.(metav1.ObjectMetaAccessor); ok {
			meta := accessor.GetObjectMeta()
			annotations := meta.GetAnnotations()
			if annotations == nil {
				return false, ""
			}
			val, found := annotations[annotationKey]
			return found, val
		}
		return false, ""
	}
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

	// Use the explicit resource name if provided, otherwise fall back to
	// the simple lowercase+s heuristic.
	if resource == "" {
		resource = strings.ToLower(kind) + "s"
	}

	return schema.GroupVersionResource{
		Group:    group,
		Version:  version,
		Resource: resource,
	}, nil
}
