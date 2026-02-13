package watcher

import (
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/bryonbaker/beacon/internal/config"
	"github.com/bryonbaker/beacon/internal/database"
	"github.com/bryonbaker/beacon/internal/metrics"
	"github.com/bryonbaker/beacon/internal/models"
)

const testAnnotationKey = "bakerapps.net.maas"

// newTestWatcher creates a Watcher wired to a MockDatabase and fake K8s clients.
func newTestWatcher(mockDB *database.MockDatabase) *Watcher {
	cfg := &config.Config{}
	cfg.Annotation.Key = testAnnotationKey
	cfg.Resources = []config.ResourceConfig{
		{APIVersion: "v1", Kind: "Pod"},
	}

	fakeClient := fake.NewSimpleClientset()
	logger := zap.NewNop()
	m := metrics.NewMetrics(prometheus.NewRegistry())

	return NewWatcher(mockDB, fakeClient, nil, cfg, m, logger)
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

func TestHandleAdd_AnnotatedPod_InsertsObject(t *testing.T) {
	mockDB := new(database.MockDatabase)
	w := newTestWatcher(mockDB)

	pod := newAnnotatedPod("my-pod", "default", "uid-123", "enabled")

	mockDB.On("InsertManagedObject", mock.MatchedBy(func(obj *models.ManagedObject) bool {
		return obj.ResourceUID == "uid-123" &&
			obj.ResourceName == "my-pod" &&
			obj.ResourceNamespace == "default" &&
			obj.AnnotationValue == "enabled" &&
			obj.DetectionSource == models.DetectionSourceWatch &&
			obj.ClusterState == models.ClusterStateExists
	})).Return(nil).Once()

	w.handleAdd(pod, "Pod", models.DetectionSourceWatch)

	mockDB.AssertExpectations(t)
}

func TestHandleAdd_UnannotatedPod_DoesNotInsert(t *testing.T) {
	mockDB := new(database.MockDatabase)
	w := newTestWatcher(mockDB)

	pod := newUnannotatedPod("my-pod", "default", "uid-456")

	// InsertManagedObject should NOT be called.
	w.handleAdd(pod, "Pod", models.DetectionSourceWatch)

	mockDB.AssertNotCalled(t, "InsertManagedObject", mock.Anything)
}

func TestHandleUpdate_AnnotationAdded_InsertsWithMutationSource(t *testing.T) {
	mockDB := new(database.MockDatabase)
	w := newTestWatcher(mockDB)

	oldPod := newUnannotatedPod("my-pod", "default", "uid-789")
	newPod := newAnnotatedPod("my-pod", "default", "uid-789", "enabled")

	mockDB.On("InsertManagedObject", mock.MatchedBy(func(obj *models.ManagedObject) bool {
		return obj.ResourceUID == "uid-789" &&
			obj.DetectionSource == models.DetectionSourceMutation &&
			obj.AnnotationValue == "enabled"
	})).Return(nil).Once()

	w.handleUpdate(oldPod, newPod, "Pod")

	mockDB.AssertExpectations(t)
}

func TestHandleUpdate_AnnotationRemoved_UpdatesClusterState(t *testing.T) {
	mockDB := new(database.MockDatabase)
	w := newTestWatcher(mockDB)

	oldPod := newAnnotatedPod("my-pod", "default", "uid-abc", "enabled")
	newPod := newUnannotatedPod("my-pod", "default", "uid-abc")

	mockDB.On("UpdateClusterState",
		"uid-abc",
		models.ClusterStateDeleted,
		mock.MatchedBy(func(t *time.Time) bool {
			return t != nil
		}),
	).Return(nil).Once()

	w.handleUpdate(oldPod, newPod, "Pod")

	mockDB.AssertExpectations(t)
}

func TestHandleDelete_TrackedPod_UpdatesClusterState(t *testing.T) {
	mockDB := new(database.MockDatabase)
	w := newTestWatcher(mockDB)

	pod := newAnnotatedPod("my-pod", "default", "uid-del", "enabled")

	// GetManagedObjectByUID returns the tracked object.
	mockDB.On("GetManagedObjectByUID", "uid-del").Return(&models.ManagedObject{
		ID:          "internal-id",
		ResourceUID: "uid-del",
	}, nil).Once()

	mockDB.On("UpdateClusterState",
		"uid-del",
		models.ClusterStateDeleted,
		mock.MatchedBy(func(t *time.Time) bool {
			return t != nil
		}),
	).Return(nil).Once()

	w.handleDelete(pod, "Pod")

	mockDB.AssertExpectations(t)
}

func TestHandleDelete_UntrackedPod_NoOp(t *testing.T) {
	mockDB := new(database.MockDatabase)
	w := newTestWatcher(mockDB)

	pod := newUnannotatedPod("my-pod", "default", "uid-untracked")

	// GetManagedObjectByUID returns nil (not tracked).
	mockDB.On("GetManagedObjectByUID", "uid-untracked").Return(nil, nil).Once()

	w.handleDelete(pod, "Pod")

	mockDB.AssertNotCalled(t, "UpdateClusterState", mock.Anything, mock.Anything, mock.Anything)
}

func TestExtractManagedObject_Pod(t *testing.T) {
	mockDB := new(database.MockDatabase)
	w := newTestWatcher(mockDB)

	pod := newAnnotatedPod("test-pod", "test-ns", "pod-uid-1", "some-value")
	pod.Labels["env"] = "test"

	mo, err := w.extractManagedObject(pod, "Pod")

	require.NoError(t, err)
	assert.Equal(t, "pod-uid-1", mo.ResourceUID)
	assert.Equal(t, "test-pod", mo.ResourceName)
	assert.Equal(t, "test-ns", mo.ResourceNamespace)
	assert.Equal(t, "Pod", mo.ResourceType)
	assert.Equal(t, "some-value", mo.AnnotationValue)
	assert.Equal(t, "1", mo.ResourceVersion)
	assert.NotEmpty(t, mo.ID, "ID should be a generated UUID")
	assert.Contains(t, mo.Labels, "env")
	assert.NotEmpty(t, mo.FullMetadata)
}

func TestExtractManagedObject_Unstructured(t *testing.T) {
	mockDB := new(database.MockDatabase)
	w := newTestWatcher(mockDB)

	obj := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "example.com/v1",
			"kind":       "MyCustomResource",
			"metadata": map[string]interface{}{
				"name":            "my-cr",
				"namespace":       "custom-ns",
				"uid":             "cr-uid-1",
				"resourceVersion": "42",
				"labels": map[string]interface{}{
					"team": "platform",
				},
				"annotations": map[string]interface{}{
					testAnnotationKey: "cr-value",
				},
			},
		},
	}

	mo, err := w.extractManagedObject(obj, "MyCustomResource")

	require.NoError(t, err)
	assert.Equal(t, "cr-uid-1", mo.ResourceUID)
	assert.Equal(t, "my-cr", mo.ResourceName)
	assert.Equal(t, "custom-ns", mo.ResourceNamespace)
	assert.Equal(t, "MyCustomResource", mo.ResourceType)
	assert.Equal(t, "cr-value", mo.AnnotationValue)
	assert.Equal(t, "42", mo.ResourceVersion)
	assert.NotEmpty(t, mo.ID, "ID should be a generated UUID")
	assert.Contains(t, mo.Labels, "team")
	assert.NotEmpty(t, mo.FullMetadata)
}

func TestHasAnnotation_Present(t *testing.T) {
	mockDB := new(database.MockDatabase)
	w := newTestWatcher(mockDB)

	pod := newAnnotatedPod("p", "ns", "uid", "val")
	ok, val := w.hasAnnotation(pod, testAnnotationKey)
	assert.True(t, ok)
	assert.Equal(t, "val", val)
}

func TestHasAnnotation_Absent(t *testing.T) {
	mockDB := new(database.MockDatabase)
	w := newTestWatcher(mockDB)

	pod := newUnannotatedPod("p", "ns", "uid")
	ok, val := w.hasAnnotation(pod, testAnnotationKey)
	assert.False(t, ok)
	assert.Empty(t, val)
}

func TestHasAnnotation_Unstructured(t *testing.T) {
	mockDB := new(database.MockDatabase)
	w := newTestWatcher(mockDB)

	obj := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"metadata": map[string]interface{}{
				"annotations": map[string]interface{}{
					testAnnotationKey: "uns-val",
				},
			},
		},
	}
	ok, val := w.hasAnnotation(obj, testAnnotationKey)
	assert.True(t, ok)
	assert.Equal(t, "uns-val", val)
}

func TestParseGVR(t *testing.T) {
	tests := []struct {
		apiVersion string
		kind       string
		wantGroup  string
		wantVer    string
		wantRes    string
	}{
		{"v1", "Pod", "", "v1", "pods"},
		{"apps/v1", "Deployment", "apps", "v1", "deployments"},
		{"example.com/v1alpha1", "Widget", "example.com", "v1alpha1", "widgets"},
	}

	for _, tt := range tests {
		t.Run(tt.apiVersion+"/"+tt.kind, func(t *testing.T) {
			gvr, err := parseGVR(tt.apiVersion, tt.kind)
			require.NoError(t, err)
			assert.Equal(t, tt.wantGroup, gvr.Group)
			assert.Equal(t, tt.wantVer, gvr.Version)
			assert.Equal(t, tt.wantRes, gvr.Resource)
		})
	}
}
