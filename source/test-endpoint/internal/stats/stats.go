// Package stats provides thread-safe event statistics tracking for the
// test-endpoint.
package stats

import (
	"sync"
	"time"
)

// Stats tracks event statistics in a thread-safe manner.
type Stats struct {
	mu                 sync.RWMutex
	totalEvents        int64
	eventsByType       map[string]int64
	eventsByResType    map[string]int64
	duplicatesDetected int64
	lastEventTimestamp time.Time
}

// New returns a new Stats instance ready for use.
func New() *Stats {
	return &Stats{
		eventsByType:    make(map[string]int64),
		eventsByResType: make(map[string]int64),
	}
}

// Record records an incoming event with the given event type and resource type.
func (s *Stats) Record(eventType, resourceType string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.totalEvents++
	s.eventsByType[eventType]++
	s.eventsByResType[resourceType]++
	s.lastEventTimestamp = time.Now()
}

// RecordDuplicate increments the duplicate detection counter.
func (s *Stats) RecordDuplicate() {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.duplicatesDetected++
}

// StatsResponse is the JSON-serialisable snapshot of current statistics.
type StatsResponse struct {
	TotalEvents        int64            `json:"total_events"`
	EventsByType       map[string]int64 `json:"events_by_type"`
	EventsByResourceType map[string]int64 `json:"events_by_resource_type"`
	DuplicatesDetected int64            `json:"duplicates_detected"`
	LastEventTimestamp  string           `json:"last_event_timestamp"`
}

// Snapshot returns a point-in-time copy of the current statistics.
func (s *Stats) Snapshot() StatsResponse {
	s.mu.RLock()
	defer s.mu.RUnlock()

	byType := make(map[string]int64, len(s.eventsByType))
	for k, v := range s.eventsByType {
		byType[k] = v
	}

	byResType := make(map[string]int64, len(s.eventsByResType))
	for k, v := range s.eventsByResType {
		byResType[k] = v
	}

	var ts string
	if !s.lastEventTimestamp.IsZero() {
		ts = s.lastEventTimestamp.Format(time.RFC3339)
	}

	return StatsResponse{
		TotalEvents:          s.totalEvents,
		EventsByType:         byType,
		EventsByResourceType: byResType,
		DuplicatesDetected:   s.duplicatesDetected,
		LastEventTimestamp:    ts,
	}
}
