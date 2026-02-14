package notifier

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/bryonbaker/beacon/internal/config"
	"github.com/bryonbaker/beacon/internal/database"
	"github.com/bryonbaker/beacon/internal/metrics"
	"github.com/bryonbaker/beacon/internal/models"
)

// testConfig returns a minimal Config suitable for unit tests.
func testConfig() *config.Config {
	return &config.Config{
		App: config.AppConfig{
			Name:    "beacon",
			Version: "0.1.0-test",
		},
		CloudEvents: config.CloudEventsConfig{
			Source:     "/beacon",
			TypePrefix: "net.bakerapps.beacon.resource",
		},
		Endpoint: config.EndpointConfig{
			URL:     "https://example.com/webhook",
			Method:  "POST",
			Timeout: config.Duration{Duration: 5 * time.Second},
			Retry: config.RetryConfig{
				MaxAttempts:       10,
				InitialBackoff:    config.Duration{Duration: 1 * time.Second},
				MaxBackoff:        config.Duration{Duration: 5 * time.Minute},
				BackoffMultiplier: 2.0,
				Jitter:            0.1,
			},
		},
		Worker: config.WorkerConfig{
			PollInterval: config.Duration{Duration: 5 * time.Second},
			BatchSize:    10,
			Concurrency:  5,
		},
	}
}

// testObject returns a ManagedObject in the pending-created state.
func testObject() *models.ManagedObject {
	return &models.ManagedObject{
		ID:                   "obj-001",
		ResourceUID:          "uid-aaa-bbb",
		ResourceType:         "ConfigMap",
		ResourceName:         "my-config",
		ResourceNamespace:    "default",
		AnnotationValue:      "enabled",
		ClusterState:         models.ClusterStateExists,
		DetectionSource:      models.DetectionSourceWatch,
		CreatedAt:            time.Now().UTC(),
		NotifiedCreated:      false,
		NotifiedDeleted:      false,
		NotificationFailed:   false,
		NotificationAttempts: 0,
		ResourceVersion:      "123",
		Labels:               `{"app":"test","env":"dev"}`,
	}
}

// newTestNotifier wires up a Notifier with mocks and an observed logger.
func newTestNotifier(cfg *config.Config, mockDB *database.MockDatabase, mockClient *MockHTTPClient) (*Notifier, *observer.ObservedLogs) {
	core, logs := observer.New(zapcore.DebugLevel)
	logger := zap.New(core)
	reg := prometheus.NewRegistry()
	m := metrics.NewMetrics(reg)
	n := NewNotifier(mockDB, mockClient, cfg, m, logger)
	return n, logs
}

// --- Tests ---

func TestHandleResponse_200_MarksNotified(t *testing.T) {
	cfg := testConfig()
	mockDB := new(database.MockDatabase)
	mockClient := new(MockHTTPClient)

	n, _ := newTestNotifier(cfg, mockDB, mockClient)

	obj := testObject()
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader("")),
	}

	mockDB.On("UpdateNotificationStatus", obj.ID, "created", mock.AnythingOfType("time.Time")).Return(nil)

	n.handleResponse(obj, "created", resp, nil)

	mockDB.AssertCalled(t, "UpdateNotificationStatus", obj.ID, "created", mock.AnythingOfType("time.Time"))
	mockDB.AssertNotCalled(t, "MarkNotificationFailed", mock.Anything, mock.Anything)
	mockDB.AssertNotCalled(t, "IncrementNotificationAttempts", mock.Anything)
}

func TestHandleResponse_500_IncrementsAttempts(t *testing.T) {
	cfg := testConfig()
	mockDB := new(database.MockDatabase)
	mockClient := new(MockHTTPClient)

	n, _ := newTestNotifier(cfg, mockDB, mockClient)

	obj := testObject()
	resp := &http.Response{
		StatusCode: http.StatusInternalServerError,
		Body:       io.NopCloser(strings.NewReader("")),
	}

	mockDB.On("IncrementNotificationAttempts", obj.ID).Return(nil)

	n.handleResponse(obj, "created", resp, nil)

	mockDB.AssertCalled(t, "IncrementNotificationAttempts", obj.ID)
	mockDB.AssertNotCalled(t, "UpdateNotificationStatus", mock.Anything, mock.Anything, mock.Anything)
	mockDB.AssertNotCalled(t, "MarkNotificationFailed", mock.Anything, mock.Anything)
}

func TestHandleResponse_400_LogsPayloadAndMarksFailed(t *testing.T) {
	cfg := testConfig()
	mockDB := new(database.MockDatabase)
	mockClient := new(MockHTTPClient)

	n, logs := newTestNotifier(cfg, mockDB, mockClient)

	obj := testObject()
	resp := &http.Response{
		StatusCode: http.StatusBadRequest,
		Body:       io.NopCloser(strings.NewReader("")),
	}

	mockDB.On("MarkNotificationFailed", obj.ID, http.StatusBadRequest).Return(nil)

	n.handleResponse(obj, "created", resp, nil)

	// Verify MarkNotificationFailed was called with the status code.
	mockDB.AssertCalled(t, "MarkNotificationFailed", obj.ID, http.StatusBadRequest)

	// Verify that an ERROR-level log was emitted containing the payload.
	errorLogs := logs.FilterLevelExact(zapcore.ErrorLevel).All()
	require.NotEmpty(t, errorLogs, "expected at least one ERROR log entry")

	found := false
	for _, entry := range errorLogs {
		if entry.Message == "non-retriable notification failure" {
			// Check that the payload field exists in the log context.
			for _, field := range entry.Context {
				if field.Key == "payload" && field.String != "" {
					found = true
					break
				}
			}
		}
	}
	assert.True(t, found, "expected ERROR log with 'payload' field for non-retriable failure")

	mockDB.AssertNotCalled(t, "UpdateNotificationStatus", mock.Anything, mock.Anything, mock.Anything)
	mockDB.AssertNotCalled(t, "IncrementNotificationAttempts", mock.Anything)
}

func TestHandleResponse_NetworkError_IncrementsAttempts(t *testing.T) {
	cfg := testConfig()
	mockDB := new(database.MockDatabase)
	mockClient := new(MockHTTPClient)

	n, _ := newTestNotifier(cfg, mockDB, mockClient)

	obj := testObject()

	mockDB.On("IncrementNotificationAttempts", obj.ID).Return(nil)

	n.handleResponse(obj, "created", nil, assert.AnError)

	mockDB.AssertCalled(t, "IncrementNotificationAttempts", obj.ID)
	mockDB.AssertNotCalled(t, "UpdateNotificationStatus", mock.Anything, mock.Anything, mock.Anything)
	mockDB.AssertNotCalled(t, "MarkNotificationFailed", mock.Anything, mock.Anything)
}

func TestCalculateBackoff_Correctness(t *testing.T) {
	initial := 1 * time.Second
	maxBack := 5 * time.Minute
	multiplier := 2.0
	jitter := 0.0 // No jitter for deterministic testing.

	tests := []struct {
		attempt  int
		expected time.Duration
	}{
		{0, 1 * time.Second},  // 1s * 2^0 = 1s
		{1, 2 * time.Second},  // 1s * 2^1 = 2s
		{2, 4 * time.Second},  // 1s * 2^2 = 4s
		{3, 8 * time.Second},  // 1s * 2^3 = 8s
		{4, 16 * time.Second}, // 1s * 2^4 = 16s
		{20, 5 * time.Minute}, // Capped at maxBackoff.
	}

	for _, tc := range tests {
		result := calculateBackoff(tc.attempt, initial, maxBack, multiplier, jitter)
		assert.Equal(t, tc.expected, result, "attempt %d", tc.attempt)
	}
}

func TestCalculateBackoff_WithJitter(t *testing.T) {
	initial := 1 * time.Second
	maxBack := 5 * time.Minute
	multiplier := 2.0
	jitter := 0.1 // +/- 10%

	// Run many iterations and confirm the result stays within expected range.
	for i := 0; i < 100; i++ {
		result := calculateBackoff(0, initial, maxBack, multiplier, jitter)
		assert.InDelta(t, float64(1*time.Second), float64(result), float64(100*time.Millisecond)+1,
			"backoff for attempt 0 should be ~1s +/- 10%%")
	}
}

func TestCalculateBackoff_CappedAtMax(t *testing.T) {
	initial := 1 * time.Second
	maxBack := 30 * time.Second
	multiplier := 2.0
	jitter := 0.0

	// 2^10 = 1024s >> 30s, so should be capped.
	result := calculateBackoff(10, initial, maxBack, multiplier, jitter)
	assert.Equal(t, maxBack, result)
}

func TestIsRetriable(t *testing.T) {
	retriable := []int{408, 429, 500, 502, 503, 504}
	for _, code := range retriable {
		assert.True(t, isRetriable(code), "expected %d to be retriable", code)
	}

	nonRetriable := []int{200, 201, 204, 301, 400, 401, 403, 404, 422}
	for _, code := range nonRetriable {
		assert.False(t, isRetriable(code), "expected %d to be non-retriable", code)
	}
}

func TestBuildRequest_ValidRequest(t *testing.T) {
	cfg := testConfig()
	mockDB := new(database.MockDatabase)
	mockClient := new(MockHTTPClient)

	n, _ := newTestNotifier(cfg, mockDB, mockClient)

	obj := testObject()
	ce := buildCloudEvent(obj, "created", cfg)

	req, err := n.buildRequest(ce)
	require.NoError(t, err)

	assert.Equal(t, http.MethodPost, req.Method)
	assert.Equal(t, "https://example.com/webhook", req.URL.String())
	assert.NotNil(t, req.Body)
}

func TestBuildRequest_Headers(t *testing.T) {
	cfg := testConfig()
	cfg.AuthToken = "test-token-123"
	cfg.Endpoint.Headers = map[string]string{
		"X-Custom-Header": "custom-value",
	}
	mockDB := new(database.MockDatabase)
	mockClient := new(MockHTTPClient)

	n, _ := newTestNotifier(cfg, mockDB, mockClient)

	obj := testObject()
	ce := buildCloudEvent(obj, "created", cfg)

	req, err := n.buildRequest(ce)
	require.NoError(t, err)

	// Content-Type must be CloudEvents structured content mode.
	assert.Equal(t, "application/cloudevents+json; charset=UTF-8", req.Header.Get("Content-Type"))

	// User-Agent
	assert.Equal(t, "beacon/0.1.0-test", req.Header.Get("User-Agent"))

	// X-Request-ID must be present and non-empty (UUID format).
	xRequestID := req.Header.Get("X-Request-ID")
	assert.NotEmpty(t, xRequestID, "X-Request-ID header should be present")
	assert.Len(t, xRequestID, 36, "X-Request-ID should be a 36-char UUID string")

	// X-Event-ID must not be present in CloudEvents mode.
	assert.Empty(t, req.Header.Get("X-Event-ID"), "X-Event-ID header should be absent")

	// Authorization bearer token.
	assert.Equal(t, "Bearer test-token-123", req.Header.Get("Authorization"))

	// Custom header from config.
	assert.Equal(t, "custom-value", req.Header.Get("X-Custom-Header"))
}

func TestBuildRequest_NoAuthTokenOmitsHeader(t *testing.T) {
	cfg := testConfig()
	cfg.AuthToken = "" // No auth token.
	mockDB := new(database.MockDatabase)
	mockClient := new(MockHTTPClient)

	n, _ := newTestNotifier(cfg, mockDB, mockClient)

	obj := testObject()
	ce := buildCloudEvent(obj, "created", cfg)

	req, err := n.buildRequest(ce)
	require.NoError(t, err)

	assert.Empty(t, req.Header.Get("Authorization"), "Authorization header should be absent when no token is configured")
}

func TestBuildCloudEvent(t *testing.T) {
	cfg := testConfig()
	obj := testObject()

	ce := buildCloudEvent(obj, "created", cfg)

	assert.Equal(t, "1.0", ce.SpecVersion)
	assert.Equal(t, obj.ID, ce.ID)
	assert.Equal(t, "/beacon/default/ConfigMap", ce.Source)
	assert.Equal(t, "net.bakerapps.beacon.resource.created", ce.Type)
	assert.Equal(t, obj.ResourceName, ce.Subject)
	assert.NotEmpty(t, ce.Time)
	assert.Equal(t, "application/json", ce.DataContentType)
	assert.Equal(t, obj.ResourceUID, ce.Data.Resource.UID)
	assert.Equal(t, obj.ResourceType, ce.Data.Resource.Type)
	assert.Equal(t, obj.ResourceName, ce.Data.Resource.Name)
	assert.Equal(t, obj.ResourceNamespace, ce.Data.Resource.Namespace)
	assert.Equal(t, obj.AnnotationValue, ce.Data.Resource.AnnotationValue)
	assert.Equal(t, obj.ResourceVersion, ce.Data.Metadata.ResourceVersion)
	assert.Equal(t, "test", ce.Data.Metadata.Labels["app"])
	assert.Equal(t, "dev", ce.Data.Metadata.Labels["env"])
}

func TestBuildCloudEvent_WithAnnotations(t *testing.T) {
	cfg := testConfig()
	obj := testObject()
	obj.Annotations = `{"example.com/customer-id":"C-12345","example.com/account":"A-67890"}`

	ce := buildCloudEvent(obj, "created", cfg)

	require.NotNil(t, ce.Data.Metadata.Annotations)
	assert.Equal(t, "C-12345", ce.Data.Metadata.Annotations["example.com/customer-id"])
	assert.Equal(t, "A-67890", ce.Data.Metadata.Annotations["example.com/account"])
}

func TestBuildCloudEvent_EmptyAnnotationsOmitted(t *testing.T) {
	cfg := testConfig()
	obj := testObject()
	obj.Annotations = ""

	ce := buildCloudEvent(obj, "created", cfg)

	assert.Nil(t, ce.Data.Metadata.Annotations)
}

func TestBuildCloudEvent_DeletedEvent(t *testing.T) {
	cfg := testConfig()
	obj := testObject()
	obj.NotifiedCreated = true
	obj.ClusterState = models.ClusterStateDeleted

	ce := buildCloudEvent(obj, "deleted", cfg)

	assert.Equal(t, "net.bakerapps.beacon.resource.deleted", ce.Type)
}

func TestBuildRequest_BodyStructure(t *testing.T) {
	cfg := testConfig()
	mockDB := new(database.MockDatabase)
	mockClient := new(MockHTTPClient)

	n, _ := newTestNotifier(cfg, mockDB, mockClient)

	obj := testObject()
	ce := buildCloudEvent(obj, "created", cfg)

	req, err := n.buildRequest(ce)
	require.NoError(t, err)

	body, err := io.ReadAll(req.Body)
	require.NoError(t, err)

	var envelope map[string]interface{}
	require.NoError(t, json.Unmarshal(body, &envelope))

	assert.Equal(t, "1.0", envelope["specversion"])
	assert.Equal(t, obj.ID, envelope["id"])
	assert.Equal(t, "/beacon/default/ConfigMap", envelope["source"])
	assert.Equal(t, "net.bakerapps.beacon.resource.created", envelope["type"])
	assert.Equal(t, obj.ResourceName, envelope["subject"])
	assert.NotNil(t, envelope["data"])
}

func TestHandleResponse_201_MarksNotified(t *testing.T) {
	cfg := testConfig()
	mockDB := new(database.MockDatabase)
	mockClient := new(MockHTTPClient)

	n, _ := newTestNotifier(cfg, mockDB, mockClient)

	obj := testObject()
	resp := &http.Response{
		StatusCode: http.StatusCreated,
		Body:       io.NopCloser(strings.NewReader("")),
	}

	mockDB.On("UpdateNotificationStatus", obj.ID, "created", mock.AnythingOfType("time.Time")).Return(nil)

	n.handleResponse(obj, "created", resp, nil)

	mockDB.AssertCalled(t, "UpdateNotificationStatus", obj.ID, "created", mock.AnythingOfType("time.Time"))
}

func TestHandleResponse_429_IncrementsAttempts(t *testing.T) {
	cfg := testConfig()
	mockDB := new(database.MockDatabase)
	mockClient := new(MockHTTPClient)

	n, _ := newTestNotifier(cfg, mockDB, mockClient)

	obj := testObject()
	resp := &http.Response{
		StatusCode: http.StatusTooManyRequests,
		Body:       io.NopCloser(strings.NewReader("")),
	}

	mockDB.On("IncrementNotificationAttempts", obj.ID).Return(nil)

	n.handleResponse(obj, "created", resp, nil)

	mockDB.AssertCalled(t, "IncrementNotificationAttempts", obj.ID)
	mockDB.AssertNotCalled(t, "MarkNotificationFailed", mock.Anything, mock.Anything)
}

func TestHandleResponse_422_MarksFailed(t *testing.T) {
	cfg := testConfig()
	mockDB := new(database.MockDatabase)
	mockClient := new(MockHTTPClient)

	n, _ := newTestNotifier(cfg, mockDB, mockClient)

	obj := testObject()
	resp := &http.Response{
		StatusCode: http.StatusUnprocessableEntity,
		Body:       io.NopCloser(strings.NewReader("")),
	}

	mockDB.On("MarkNotificationFailed", obj.ID, http.StatusUnprocessableEntity).Return(nil)

	n.handleResponse(obj, "created", resp, nil)

	mockDB.AssertCalled(t, "MarkNotificationFailed", obj.ID, http.StatusUnprocessableEntity)
}
