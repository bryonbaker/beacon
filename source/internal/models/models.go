// Package models defines the data structures used throughout the beacon service.
package models

import (
	"time"
)

// Cluster state constants
const (
	ClusterStateExists  = "exists"
	ClusterStateDeleted = "deleted"
)

// Detection source constants
const (
	DetectionSourceWatch          = "watch"
	DetectionSourceMutation       = "mutation"
	DetectionSourceReconciliation = "reconciliation"
)

// Notification status constants
const (
	NotificationPending = "pending"
	NotificationSent    = "sent"
	NotificationFailed  = "failed"
)

// ManagedObject represents a Kubernetes resource tracked by beacon.
// It mirrors the managed_objects database table.
type ManagedObject struct {
	ID                        string     `json:"id"`
	ResourceUID               string     `json:"resource_uid"`
	ResourceType              string     `json:"resource_type"`
	ResourceName              string     `json:"resource_name"`
	ResourceNamespace         string     `json:"resource_namespace"`
	AnnotationValue           string     `json:"annotation_value"`
	ClusterState              string     `json:"cluster_state"`
	DetectionSource           string     `json:"detection_source"`
	CreatedAt                 time.Time  `json:"created_at"`
	DeletedAt                 *time.Time `json:"deleted_at,omitempty"`
	LastReconciled            *time.Time `json:"last_reconciled,omitempty"`
	NotifiedCreated           bool       `json:"notified_created"`
	NotifiedDeleted           bool       `json:"notified_deleted"`
	NotificationFailed        bool       `json:"notification_failed"`
	NotificationFailedCode    int        `json:"notification_failed_status_code,omitempty"`
	CreatedNotificationSentAt *time.Time `json:"created_notification_sent_at,omitempty"`
	DeletedNotificationSentAt *time.Time `json:"deleted_notification_sent_at,omitempty"`
	NotificationAttempts      int        `json:"notification_attempts"`
	LastNotificationAttempt   *time.Time `json:"last_notification_attempt,omitempty"`
	Labels                    string     `json:"labels,omitempty"`
	ResourceVersion           string     `json:"resource_version,omitempty"`
	FullMetadata              string     `json:"full_metadata,omitempty"`
}

// IsPendingCreationNotification returns true if a creation notification has not been sent
// and the notification has not permanently failed.
func (m *ManagedObject) IsPendingCreationNotification() bool {
	return !m.NotifiedCreated && !m.NotificationFailed
}

// IsPendingDeletionNotification returns true if the object is deleted, the deletion
// notification has not been sent, and the notification has not permanently failed.
func (m *ManagedObject) IsPendingDeletionNotification() bool {
	return m.ClusterState == ClusterStateDeleted && !m.NotifiedDeleted && !m.NotificationFailed
}

// IsEligibleForCleanup returns true if the record can be cleaned up:
// the object is deleted, both notifications have been sent, and the notification has not failed.
func (m *ManagedObject) IsEligibleForCleanup(retentionPeriod time.Duration) bool {
	if m.ClusterState != ClusterStateDeleted {
		return false
	}
	if !m.NotifiedDeleted {
		return false
	}
	if m.NotificationFailed {
		return false
	}
	if m.DeletedAt == nil {
		return false
	}
	return time.Since(*m.DeletedAt) > retentionPeriod
}

// NotificationPayload is the JSON body sent to the notification endpoint.
type NotificationPayload struct {
	ID        string               `json:"id"`
	Timestamp string               `json:"timestamp"`
	EventType string               `json:"eventType"`
	Resource  NotificationResource `json:"resource"`
	Metadata  NotificationMetadata `json:"metadata"`
}

// NotificationResource describes the Kubernetes resource in a notification payload.
type NotificationResource struct {
	UID             string `json:"uid"`
	Type            string `json:"type"`
	Name            string `json:"name"`
	Namespace       string `json:"namespace"`
	AnnotationValue string `json:"annotationValue"`
}

// NotificationMetadata contains additional metadata for a notification payload.
type NotificationMetadata struct {
	Labels          map[string]string `json:"labels,omitempty"`
	ResourceVersion string            `json:"resourceVersion,omitempty"`
}

// HealthResponse is returned by the /healthz liveness endpoint.
type HealthResponse struct {
	Status    string `json:"status"`
	Timestamp string `json:"timestamp"`
}

// ReadinessResponse is returned by the /ready readiness endpoint.
type ReadinessResponse struct {
	Status    string            `json:"status"`
	Timestamp string            `json:"timestamp"`
	Checks    map[string]string `json:"checks"`
}
