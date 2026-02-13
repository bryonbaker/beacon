package reconciler

import (
	"context"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/bryonbaker/beacon/internal/config"
	"github.com/bryonbaker/beacon/internal/database"
	"github.com/bryonbaker/beacon/internal/metrics"
	"github.com/bryonbaker/beacon/internal/models"
)

const testAnnotationKey = "bakerapps.net.maas"

// newTestReconciler creates a Reconciler wired to a MockDatabase and a fake
// typed Kubernetes client pre-loaded with the given pods.
func newTestReconciler(mockDB *database.MockDatabase, pods ...runtime.Object) *Reconciler {
	cfg := &config.Config{}
	cfg.Annotation.Key = testAnnotationKey
	cfg.Resources = []config.ResourceConfig{
		{APIVersion: "v1", Kind: "Pod"},
	}
	cfg.Reconciliation.Enabled = true
	cfg.Reconciliation.OnStartup = false
	cfg.Reconciliation.Interval.Duration = 15 * time.Minute
	cfg.Reconciliation.Timeout.Duration = 10 * time.Minute

	fakeClient := fake.NewSimpleClientset(pods...)
	logger := zap.NewNop()
	m := metrics.NewMetrics(prometheus.NewRegistry())

	return NewReconciler(mockDB, fakeClient, nil, cfg, m, logger)
}

// newAnnotatedPod creates a Pod with the tracking annotation set.
func newAnnotatedPod(name, namespace, uid, annotationValue string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			UID:       types.UID(uid),
			Annotations: map[string]string{
				testAnnotationKey: annotationValue,
			},
			Labels: map[string]string{
				"app": name,
			},
			ResourceVersion: "1",
		},
	}
}

// newUnannotatedPod creates a Pod without the tracking annotation.
func newUnannotatedPod(name, namespace, uid string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:            name,
			Namespace:       namespace,
			UID:             types.UID(uid),
			ResourceVersion: "1",
		},
	}
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func TestReconcile_NoDrift(t *testing.T) {
	// Cluster has one annotated pod, DB has one active object with the same UID.
	pod := newAnnotatedPod("my-pod", "default", "uid-1", "enabled")
	mockDB := new(database.MockDatabase)
	r := newTestReconciler(mockDB, pod)

	dbObj := &models.ManagedObject{
		ID:          "db-id-1",
		ResourceUID: "uid-1",
		ResourceType: "Pod",
		ResourceName: "my-pod",
		ResourceNamespace: "default",
		ClusterState: models.ClusterStateExists,
	}

	mockDB.On("GetAllActiveObjects", "Pod").Return([]*models.ManagedObject{dbObj}, nil).Once()
	mockDB.On("UpdateLastReconciled", "db-id-1", mock.AnythingOfType("time.Time")).Return(nil).Once()

	err := r.Reconcile(context.Background())

	require.NoError(t, err)
	mockDB.AssertExpectations(t)
	// No InsertManagedObject or UpdateClusterState calls expected.
	mockDB.AssertNotCalled(t, "InsertManagedObject", mock.Anything)
	mockDB.AssertNotCalled(t, "UpdateClusterState", mock.Anything, mock.Anything, mock.Anything)
}

func TestReconcile_MissedCreation(t *testing.T) {
	// Cluster has an annotated pod that is NOT in the database.
	pod := newAnnotatedPod("new-pod", "default", "uid-new", "enabled")
	mockDB := new(database.MockDatabase)
	r := newTestReconciler(mockDB, pod)

	// DB returns empty list for this resource type.
	mockDB.On("GetAllActiveObjects", "Pod").Return([]*models.ManagedObject{}, nil).Once()
	mockDB.On("InsertManagedObject", mock.MatchedBy(func(obj *models.ManagedObject) bool {
		return obj.ResourceUID == "uid-new" &&
			obj.ResourceName == "new-pod" &&
			obj.ResourceNamespace == "default" &&
			obj.DetectionSource == models.DetectionSourceReconciliation &&
			obj.ClusterState == models.ClusterStateExists
	})).Return(nil).Once()

	err := r.Reconcile(context.Background())

	require.NoError(t, err)
	mockDB.AssertExpectations(t)
}

func TestReconcile_MissedDeletion(t *testing.T) {
	// Cluster has no annotated pods, but DB has one active object.
	mockDB := new(database.MockDatabase)
	// No pods in the cluster (only unannotated one).
	unannotated := newUnannotatedPod("other-pod", "default", "uid-other")
	r := newTestReconciler(mockDB, unannotated)

	dbObj := &models.ManagedObject{
		ID:                "db-id-gone",
		ResourceUID:       "uid-gone",
		ResourceType:      "Pod",
		ResourceName:      "gone-pod",
		ResourceNamespace: "default",
		ClusterState:      models.ClusterStateExists,
	}

	mockDB.On("GetAllActiveObjects", "Pod").Return([]*models.ManagedObject{dbObj}, nil).Once()
	mockDB.On("UpdateClusterState",
		"uid-gone",
		models.ClusterStateDeleted,
		mock.MatchedBy(func(t *time.Time) bool {
			return t != nil && time.Since(*t) < 5*time.Second
		}),
	).Return(nil).Once()

	err := r.Reconcile(context.Background())

	require.NoError(t, err)
	mockDB.AssertExpectations(t)
}

func TestReconcile_ContextCancellation(t *testing.T) {
	// Verify that Start stops when the context is cancelled.
	mockDB := new(database.MockDatabase)
	r := newTestReconciler(mockDB)

	cfg := r.cfg
	cfg.Reconciliation.OnStartup = false
	cfg.Reconciliation.Interval.Duration = 50 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		r.Start(ctx)
		close(done)
	}()

	// Cancel after a short delay to allow at most one tick.
	time.Sleep(30 * time.Millisecond)
	cancel()

	select {
	case <-done:
		// Start returned as expected.
	case <-time.After(2 * time.Second):
		t.Fatal("Start did not return after context cancellation")
	}
}

func TestReconcile_MixedDrift(t *testing.T) {
	// Cluster has pod-A (annotated) and pod-C (annotated).
	// DB has pod-A and pod-B.
	// Expected: pod-C = missed creation, pod-B = missed deletion, pod-A = reconciled.
	podA := newAnnotatedPod("pod-a", "default", "uid-a", "enabled")
	podC := newAnnotatedPod("pod-c", "default", "uid-c", "enabled")
	mockDB := new(database.MockDatabase)
	r := newTestReconciler(mockDB, podA, podC)

	dbObjA := &models.ManagedObject{
		ID:                "db-id-a",
		ResourceUID:       "uid-a",
		ResourceType:      "Pod",
		ResourceName:      "pod-a",
		ResourceNamespace: "default",
		ClusterState:      models.ClusterStateExists,
	}
	dbObjB := &models.ManagedObject{
		ID:                "db-id-b",
		ResourceUID:       "uid-b",
		ResourceType:      "Pod",
		ResourceName:      "pod-b",
		ResourceNamespace: "default",
		ClusterState:      models.ClusterStateExists,
	}

	mockDB.On("GetAllActiveObjects", "Pod").Return([]*models.ManagedObject{dbObjA, dbObjB}, nil).Once()

	// Missed creation: pod-C inserted.
	mockDB.On("InsertManagedObject", mock.MatchedBy(func(obj *models.ManagedObject) bool {
		return obj.ResourceUID == "uid-c" &&
			obj.DetectionSource == models.DetectionSourceReconciliation
	})).Return(nil).Once()

	// Missed deletion: pod-B marked deleted.
	mockDB.On("UpdateClusterState",
		"uid-b",
		models.ClusterStateDeleted,
		mock.MatchedBy(func(t *time.Time) bool {
			return t != nil
		}),
	).Return(nil).Once()

	// pod-A still exists, update last_reconciled.
	mockDB.On("UpdateLastReconciled", "db-id-a", mock.AnythingOfType("time.Time")).Return(nil).Once()

	err := r.Reconcile(context.Background())

	require.NoError(t, err)
	mockDB.AssertExpectations(t)
}

func TestReconcile_SkipsUnannotatedPods(t *testing.T) {
	// Cluster has one unannotated pod only. DB is empty.
	// No drift should be reported.
	unannotated := newUnannotatedPod("plain-pod", "default", "uid-plain")
	mockDB := new(database.MockDatabase)
	r := newTestReconciler(mockDB, unannotated)

	mockDB.On("GetAllActiveObjects", "Pod").Return([]*models.ManagedObject{}, nil).Once()

	err := r.Reconcile(context.Background())

	require.NoError(t, err)
	mockDB.AssertExpectations(t)
	mockDB.AssertNotCalled(t, "InsertManagedObject", mock.Anything)
	mockDB.AssertNotCalled(t, "UpdateClusterState", mock.Anything, mock.Anything, mock.Anything)
}

func TestNewReconciler_ReturnsNonNil(t *testing.T) {
	mockDB := new(database.MockDatabase)
	r := newTestReconciler(mockDB)

	assert.NotNil(t, r)
	assert.NotNil(t, r.db)
	assert.NotNil(t, r.typedClient)
	assert.NotNil(t, r.cfg)
	assert.NotNil(t, r.metrics)
	assert.NotNil(t, r.logger)
}
