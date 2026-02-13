package database

import (
	"time"

	"github.com/bryonbaker/beacon/internal/models"
	"github.com/stretchr/testify/mock"
)

// MockDatabase is a testify/mock implementation of the Database interface.
type MockDatabase struct {
	mock.Mock
}

// Ensure MockDatabase satisfies the Database interface at compile time.
var _ Database = (*MockDatabase)(nil)

// Close mocks the Close method.
func (m *MockDatabase) Close() error {
	args := m.Called()
	return args.Error(0)
}

// Ping mocks the Ping method.
func (m *MockDatabase) Ping() error {
	args := m.Called()
	return args.Error(0)
}

// InsertManagedObject mocks the InsertManagedObject method.
func (m *MockDatabase) InsertManagedObject(obj *models.ManagedObject) error {
	args := m.Called(obj)
	return args.Error(0)
}

// GetManagedObjectByUID mocks the GetManagedObjectByUID method.
func (m *MockDatabase) GetManagedObjectByUID(uid string) (*models.ManagedObject, error) {
	args := m.Called(uid)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.ManagedObject), args.Error(1)
}

// GetManagedObjectByID mocks the GetManagedObjectByID method.
func (m *MockDatabase) GetManagedObjectByID(id string) (*models.ManagedObject, error) {
	args := m.Called(id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.ManagedObject), args.Error(1)
}

// UpdateClusterState mocks the UpdateClusterState method.
func (m *MockDatabase) UpdateClusterState(uid string, state string, deletedAt *time.Time) error {
	args := m.Called(uid, state, deletedAt)
	return args.Error(0)
}

// UpdateNotificationStatus mocks the UpdateNotificationStatus method.
func (m *MockDatabase) UpdateNotificationStatus(id string, eventType string, sentAt time.Time) error {
	args := m.Called(id, eventType, sentAt)
	return args.Error(0)
}

// MarkNotificationFailed mocks the MarkNotificationFailed method.
func (m *MockDatabase) MarkNotificationFailed(id string, statusCode int) error {
	args := m.Called(id, statusCode)
	return args.Error(0)
}

// IncrementNotificationAttempts mocks the IncrementNotificationAttempts method.
func (m *MockDatabase) IncrementNotificationAttempts(id string) error {
	args := m.Called(id)
	return args.Error(0)
}

// UpdateLastReconciled mocks the UpdateLastReconciled method.
func (m *MockDatabase) UpdateLastReconciled(id string, reconciledAt time.Time) error {
	args := m.Called(id, reconciledAt)
	return args.Error(0)
}

// GetPendingNotifications mocks the GetPendingNotifications method.
func (m *MockDatabase) GetPendingNotifications(limit int) ([]*models.ManagedObject, error) {
	args := m.Called(limit)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*models.ManagedObject), args.Error(1)
}

// GetAllActiveObjects mocks the GetAllActiveObjects method.
func (m *MockDatabase) GetAllActiveObjects(resourceType string) ([]*models.ManagedObject, error) {
	args := m.Called(resourceType)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*models.ManagedObject), args.Error(1)
}

// GetCleanupEligible mocks the GetCleanupEligible method.
func (m *MockDatabase) GetCleanupEligible(retentionPeriod time.Duration) ([]*models.ManagedObject, error) {
	args := m.Called(retentionPeriod)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*models.ManagedObject), args.Error(1)
}

// CountByState mocks the CountByState method.
func (m *MockDatabase) CountByState() (int, int, error) {
	args := m.Called()
	return args.Int(0), args.Int(1), args.Error(2)
}

// DeleteRecord mocks the DeleteRecord method.
func (m *MockDatabase) DeleteRecord(id string) error {
	args := m.Called(id)
	return args.Error(0)
}

// RunIncrementalVacuum mocks the RunIncrementalVacuum method.
func (m *MockDatabase) RunIncrementalVacuum() error {
	args := m.Called()
	return args.Error(0)
}

// GetDatabaseSizeBytes mocks the GetDatabaseSizeBytes method.
func (m *MockDatabase) GetDatabaseSizeBytes() (int64, error) {
	args := m.Called()
	return args.Get(0).(int64), args.Error(1)
}
