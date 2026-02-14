// Package notifier implements the notification worker that polls the database
// for pending managed-object events and delivers them to the configured HTTP
// endpoint with exponential-backoff retry logic.
package notifier

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"math"
	mrand "math/rand"
	"net/http"
	"time"

	"go.uber.org/zap"

	"github.com/bryonbaker/beacon/internal/config"
	"github.com/bryonbaker/beacon/internal/database"
	"github.com/bryonbaker/beacon/internal/metrics"
	"github.com/bryonbaker/beacon/internal/models"
)

// HTTPClient is the interface used to send HTTP requests. *http.Client satisfies
// this interface, and it can be replaced with a mock in tests.
type HTTPClient interface {
	Do(req *http.Request) (*http.Response, error)
}

// Notifier polls the database for managed objects that need notification and
// delivers the corresponding event to the configured endpoint.
type Notifier struct {
	db      database.Database
	client  HTTPClient
	cfg     *config.Config
	metrics *metrics.Metrics
	logger  *zap.Logger
}

// NewNotifier creates a Notifier with the given dependencies.
func NewNotifier(db database.Database, client HTTPClient, cfg *config.Config, m *metrics.Metrics, logger *zap.Logger) *Notifier {
	return &Notifier{
		db:      db,
		client:  client,
		cfg:     cfg,
		metrics: m,
		logger:  logger,
	}
}

// Start begins the notification polling loop. It fetches pending notifications
// from the database at every PollInterval and processes each one. The loop
// stops when ctx is cancelled.
func (n *Notifier) Start(ctx context.Context) {
	ticker := time.NewTicker(n.cfg.Worker.PollInterval.Duration)
	defer ticker.Stop()

	n.logger.Info("notifier started",
		zap.Duration("poll_interval", n.cfg.Worker.PollInterval.Duration),
		zap.Int("batch_size", n.cfg.Worker.BatchSize),
	)

	for {
		select {
		case <-ctx.Done():
			n.logger.Info("notifier stopping", zap.Error(ctx.Err()))
			return
		case <-ticker.C:
			n.poll(ctx)
		}
	}
}

// poll fetches a batch of pending notifications and processes each one.
func (n *Notifier) poll(ctx context.Context) {
	pending, err := n.db.GetPendingNotifications(n.cfg.Worker.BatchSize)
	if err != nil {
		n.logger.Error("failed to fetch pending notifications", zap.Error(err))
		return
	}

	for _, obj := range pending {
		select {
		case <-ctx.Done():
			return
		default:
			n.processNotification(ctx, obj)
		}
	}
}

// processNotification determines the event type, builds the payload, sends the
// HTTP request, and handles the response.
func (n *Notifier) processNotification(ctx context.Context, obj *models.ManagedObject) {
	// Determine event type.
	var eventType string
	if !obj.NotifiedCreated {
		eventType = "created"
	} else if obj.ClusterState == models.ClusterStateDeleted && !obj.NotifiedDeleted {
		eventType = "deleted"
	} else {
		// Nothing to notify.
		return
	}

	// Build the notification payload.
	payload := buildPayload(obj, eventType)

	// Build the HTTP request.
	req, err := n.buildRequest(payload)
	if err != nil {
		n.logger.Error("failed to build notification request",
			zap.String("object_id", obj.ID),
			zap.Error(err),
		)
		return
	}

	// Send the request with the configured timeout.
	sendCtx, cancel := context.WithTimeout(ctx, n.cfg.Endpoint.Timeout.Duration)
	defer cancel()
	req = req.WithContext(sendCtx)

	resp, sendErr := n.client.Do(req)
	if resp != nil && resp.Body != nil {
		defer resp.Body.Close()
	}

	n.handleResponse(obj, eventType, resp, sendErr)
}

// buildPayload constructs a NotificationPayload from a ManagedObject.
func buildPayload(obj *models.ManagedObject, eventType string) *models.NotificationPayload {
	payload := &models.NotificationPayload{
		ID:        obj.ID,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		EventType: eventType,
		Resource: models.NotificationResource{
			UID:             obj.ResourceUID,
			Type:            obj.ResourceType,
			Name:            obj.ResourceName,
			Namespace:       obj.ResourceNamespace,
			AnnotationValue: obj.AnnotationValue,
		},
		Metadata: models.NotificationMetadata{
			ResourceVersion: obj.ResourceVersion,
		},
	}

	// Parse annotations JSON into map if present.
	if obj.Annotations != "" {
		var annotations map[string]string
		if err := json.Unmarshal([]byte(obj.Annotations), &annotations); err == nil && len(annotations) > 0 {
			payload.Metadata.Annotations = annotations
		}
	}

	// Parse labels JSON into map if present.
	if obj.Labels != "" {
		var labels map[string]string
		if err := json.Unmarshal([]byte(obj.Labels), &labels); err == nil {
			payload.Metadata.Labels = labels
		}
	}

	return payload
}

// handleResponse inspects the HTTP response (or error) and updates the
// database and metrics accordingly.
func (n *Notifier) handleResponse(obj *models.ManagedObject, eventType string, resp *http.Response, err error) {
	// Network error or timeout: treat as retriable.
	if err != nil {
		n.logger.Warn("notification request failed",
			zap.String("object_id", obj.ID),
			zap.String("event_type", eventType),
			zap.Error(err),
		)
		n.incrementAttempts(obj)
		n.metrics.RecordEndpointHealth(false)
		return
	}

	statusCode := resp.StatusCode

	switch {
	case statusCode >= 200 && statusCode < 300:
		// Success: mark as notified.
		if dbErr := n.db.UpdateNotificationStatus(obj.ID, eventType, time.Now().UTC()); dbErr != nil {
			n.logger.Error("failed to update notification status",
				zap.String("object_id", obj.ID),
				zap.Error(dbErr),
			)
		}
		n.logger.Info("notification sent successfully",
			zap.String("object_id", obj.ID),
			zap.String("event_type", eventType),
			zap.Int("status_code", statusCode),
		)
		n.metrics.RecordNotificationSent(eventType)
		n.metrics.RecordEndpointHealth(true)

	case isRetriable(statusCode):
		// Retriable server/rate-limit error: increment attempts with backoff.
		backoff := calculateBackoff(
			obj.NotificationAttempts,
			n.cfg.Endpoint.Retry.InitialBackoff.Duration,
			n.cfg.Endpoint.Retry.MaxBackoff.Duration,
			n.cfg.Endpoint.Retry.BackoffMultiplier,
			n.cfg.Endpoint.Retry.Jitter,
		)
		n.logger.Warn("retriable notification failure",
			zap.String("object_id", obj.ID),
			zap.String("event_type", eventType),
			zap.Int("status_code", statusCode),
			zap.Int("attempt", obj.NotificationAttempts+1),
			zap.Duration("next_backoff", backoff),
		)
		n.incrementAttempts(obj)
		n.metrics.RecordEndpointHealth(false)

	default:
		// Non-retriable client error (400, 401, 403, 404, 422, etc.).
		payload := buildPayload(obj, eventType)
		payloadBytes, _ := json.Marshal(payload)
		n.logger.Error("non-retriable notification failure",
			zap.String("object_id", obj.ID),
			zap.String("event_type", eventType),
			zap.Int("status_code", statusCode),
			zap.String("payload", string(payloadBytes)),
		)
		if dbErr := n.db.MarkNotificationFailed(obj.ID, statusCode); dbErr != nil {
			n.logger.Error("failed to mark notification as failed",
				zap.String("object_id", obj.ID),
				zap.Error(dbErr),
			)
		}
		n.metrics.RecordNotificationFailed(eventType, statusCode)
		n.metrics.RecordEndpointHealth(false)
	}
}

// incrementAttempts bumps the notification attempt counter in the database.
func (n *Notifier) incrementAttempts(obj *models.ManagedObject) {
	if err := n.db.IncrementNotificationAttempts(obj.ID); err != nil {
		n.logger.Error("failed to increment notification attempts",
			zap.String("object_id", obj.ID),
			zap.Error(err),
		)
	}
}

// calculateBackoff computes the next backoff duration using exponential
// backoff with jitter.
//
// Formula: min(initialBackoff * multiplier^attempt, maxBackoff) +/- jitter%
func calculateBackoff(attempt int, initialBackoff, maxBackoff time.Duration, multiplier, jitter float64) time.Duration {
	backoff := float64(initialBackoff) * math.Pow(multiplier, float64(attempt))
	if backoff > float64(maxBackoff) {
		backoff = float64(maxBackoff)
	}

	// Apply jitter: random variation of +/- jitter%.
	jitterRange := backoff * jitter
	// nolint: gosec // jitter does not need cryptographic randomness.
	backoff = backoff + (mrand.Float64()*2-1)*jitterRange

	if backoff < 0 {
		backoff = 0
	}

	return time.Duration(backoff)
}

// isRetriable returns true for HTTP status codes that indicate a transient
// failure worth retrying.
func isRetriable(statusCode int) bool {
	switch statusCode {
	case http.StatusRequestTimeout,       // 408
		http.StatusTooManyRequests,         // 429
		http.StatusInternalServerError,     // 500
		http.StatusBadGateway,             // 502
		http.StatusServiceUnavailable,     // 503
		http.StatusGatewayTimeout:         // 504
		return true
	default:
		return false
	}
}

// buildRequest constructs the HTTP POST request for the notification payload.
func (n *Notifier) buildRequest(payload *models.NotificationPayload) (*http.Request, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshalling notification payload: %w", err)
	}

	req, err := http.NewRequest(http.MethodPost, n.cfg.Endpoint.URL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("creating HTTP request: %w", err)
	}

	// Standard headers.
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", fmt.Sprintf("beacon/%s", n.cfg.App.Version))
	req.Header.Set("X-Request-ID", newUUID())
	req.Header.Set("X-Event-ID", payload.ID)

	// Bearer token authentication.
	if n.cfg.AuthToken != "" {
		req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", n.cfg.AuthToken))
	}

	// Custom headers from configuration.
	for k, v := range n.cfg.Endpoint.Headers {
		req.Header.Set(k, v)
	}

	return req, nil
}

// newUUID generates a version-4 UUID string without requiring an external
// dependency.
func newUUID() string {
	var uuid [16]byte
	_, _ = rand.Read(uuid[:])
	// Set version 4 and variant bits per RFC 4122.
	uuid[6] = (uuid[6] & 0x0f) | 0x40
	uuid[8] = (uuid[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		uuid[0:4], uuid[4:6], uuid[6:8], uuid[8:10], uuid[10:16])
}
