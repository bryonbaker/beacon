package models

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestIsPendingCreationNotification(t *testing.T) {
	tests := []struct {
		name     string
		obj      ManagedObject
		expected bool
	}{
		{
			name:     "pending when not notified and not failed",
			obj:      ManagedObject{NotifiedCreated: false, NotificationFailed: false},
			expected: true,
		},
		{
			name:     "not pending when already notified",
			obj:      ManagedObject{NotifiedCreated: true, NotificationFailed: false},
			expected: false,
		},
		{
			name:     "not pending when notification failed",
			obj:      ManagedObject{NotifiedCreated: false, NotificationFailed: true},
			expected: false,
		},
		{
			name:     "not pending when both notified and failed",
			obj:      ManagedObject{NotifiedCreated: true, NotificationFailed: true},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.obj.IsPendingCreationNotification())
		})
	}
}

func TestIsPendingDeletionNotification(t *testing.T) {
	tests := []struct {
		name     string
		obj      ManagedObject
		expected bool
	}{
		{
			name: "pending when deleted, not notified, not failed",
			obj: ManagedObject{
				ClusterState:       ClusterStateDeleted,
				NotifiedDeleted:    false,
				NotificationFailed: false,
			},
			expected: true,
		},
		{
			name: "not pending when still exists",
			obj: ManagedObject{
				ClusterState:       ClusterStateExists,
				NotifiedDeleted:    false,
				NotificationFailed: false,
			},
			expected: false,
		},
		{
			name: "not pending when already notified",
			obj: ManagedObject{
				ClusterState:       ClusterStateDeleted,
				NotifiedDeleted:    true,
				NotificationFailed: false,
			},
			expected: false,
		},
		{
			name: "not pending when notification failed",
			obj: ManagedObject{
				ClusterState:       ClusterStateDeleted,
				NotifiedDeleted:    false,
				NotificationFailed: true,
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.obj.IsPendingDeletionNotification())
		})
	}
}

func TestIsEligibleForCleanup(t *testing.T) {
	now := time.Now()
	oldTime := now.Add(-72 * time.Hour)
	recentTime := now.Add(-1 * time.Hour)
	retention := 48 * time.Hour

	tests := []struct {
		name     string
		obj      ManagedObject
		expected bool
	}{
		{
			name: "eligible when deleted, notified, not failed, past retention",
			obj: ManagedObject{
				ClusterState:       ClusterStateDeleted,
				NotifiedDeleted:    true,
				NotificationFailed: false,
				DeletedAt:          &oldTime,
			},
			expected: true,
		},
		{
			name: "not eligible when still exists",
			obj: ManagedObject{
				ClusterState:       ClusterStateExists,
				NotifiedDeleted:    true,
				NotificationFailed: false,
				DeletedAt:          &oldTime,
			},
			expected: false,
		},
		{
			name: "not eligible when not notified",
			obj: ManagedObject{
				ClusterState:       ClusterStateDeleted,
				NotifiedDeleted:    false,
				NotificationFailed: false,
				DeletedAt:          &oldTime,
			},
			expected: false,
		},
		{
			name: "not eligible when notification failed",
			obj: ManagedObject{
				ClusterState:       ClusterStateDeleted,
				NotifiedDeleted:    true,
				NotificationFailed: true,
				DeletedAt:          &oldTime,
			},
			expected: false,
		},
		{
			name: "not eligible when within retention period",
			obj: ManagedObject{
				ClusterState:       ClusterStateDeleted,
				NotifiedDeleted:    true,
				NotificationFailed: false,
				DeletedAt:          &recentTime,
			},
			expected: false,
		},
		{
			name: "not eligible when deleted_at is nil",
			obj: ManagedObject{
				ClusterState:       ClusterStateDeleted,
				NotifiedDeleted:    true,
				NotificationFailed: false,
				DeletedAt:          nil,
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.obj.IsEligibleForCleanup(retention))
		})
	}
}

func TestConstants(t *testing.T) {
	assert.Equal(t, "exists", ClusterStateExists)
	assert.Equal(t, "deleted", ClusterStateDeleted)
	assert.Equal(t, "watch", DetectionSourceWatch)
	assert.Equal(t, "mutation", DetectionSourceMutation)
	assert.Equal(t, "reconciliation", DetectionSourceReconciliation)
}
