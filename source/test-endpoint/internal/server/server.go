// Package server implements the HTTP handlers for the test-endpoint, including
// event ingestion, stats reporting, and health checking.
package server

import (
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"sync"
	"time"

	"github.com/bryonbaker/beacon/test-endpoint/internal/config"
	"github.com/bryonbaker/beacon/test-endpoint/internal/stats"
)

// EventPayload represents the JSON body sent by Beacon. The structure mirrors
// models.NotificationPayload from the main beacon service.
type EventPayload struct {
	ID        string        `json:"id"`
	Timestamp string        `json:"timestamp"`
	EventType string        `json:"eventType"`
	Resource  EventResource `json:"resource"`
	Metadata  EventMetadata `json:"metadata"`
}

// EventResource contains the Kubernetes resource details within a notification.
type EventResource struct {
	UID             string `json:"uid"`
	Type            string `json:"type"`
	Name            string `json:"name"`
	Namespace       string `json:"namespace"`
	AnnotationValue string `json:"annotationValue"`
}

// EventMetadata contains additional metadata within a notification.
type EventMetadata struct {
	Annotations     map[string]string `json:"annotations,omitempty"`
	Labels          map[string]string `json:"labels,omitempty"`
	ResourceVersion string            `json:"resourceVersion,omitempty"`
}

// Server is the test-endpoint HTTP server.
type Server struct {
	cfg   config.Config
	stats *stats.Stats
	mux   *http.ServeMux

	// idempotency tracking
	seenMu   sync.Mutex
	seenIDs  map[string]struct{}
	seenList []string // ring buffer for eviction
}

// New creates a new Server with the given configuration.
func New(cfg config.Config) *Server {
	s := &Server{
		cfg:     cfg,
		stats:   stats.New(),
		mux:     http.NewServeMux(),
		seenIDs: make(map[string]struct{}),
	}

	s.mux.HandleFunc(cfg.Server.Path, s.handleEvent)
	s.mux.HandleFunc("/stats", s.handleStats)
	s.mux.HandleFunc("/health", s.handleHealth)

	return s
}

// Handler returns the http.Handler for use with an http.Server.
func (s *Server) Handler() http.Handler {
	return s.mux
}

// handleHealth returns a simple health check response.
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, `{"status":"ok"}`)
}

// handleStats returns the current event statistics as JSON.
func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	snap := s.stats.Snapshot()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(snap); err != nil {
		s.logError("failed to encode stats response: %v", err)
	}
}

// handleEvent processes incoming event notifications.
func (s *Server) handleEvent(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	// Validate Content-Type
	ct := r.Header.Get("Content-Type")
	if ct != "application/json" {
		http.Error(w, `{"error":"Content-Type must be application/json"}`, http.StatusUnsupportedMediaType)
		return
	}

	// Validate required headers
	eventID := r.Header.Get("X-Event-ID")
	if eventID == "" {
		http.Error(w, `{"error":"missing required header: X-Event-ID"}`, http.StatusBadRequest)
		return
	}
	requestID := r.Header.Get("X-Request-ID")
	if requestID == "" {
		http.Error(w, `{"error":"missing required header: X-Request-ID"}`, http.StatusBadRequest)
		return
	}

	// Log headers if configured
	if s.cfg.Logging.IncludeHeaders {
		s.logInfo("headers: X-Event-ID=%s X-Request-ID=%s", eventID, requestID)
	}

	// Parse JSON body
	var payload EventPayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"invalid JSON: %s"}`, err.Error()), http.StatusBadRequest)
		return
	}

	// Log body if configured
	if s.cfg.Logging.IncludeBody {
		s.logInfo("event: id=%s type=%s resource=%s/%s namespace=%s name=%s annotation=%s",
			payload.ID, payload.EventType, payload.Resource.Type, payload.Resource.UID,
			payload.Resource.Namespace, payload.Resource.Name, payload.Resource.AnnotationValue)
	}

	// Idempotency check
	if s.cfg.Idempotency.Enabled {
		if s.isDuplicate(eventID) {
			s.stats.RecordDuplicate()
			s.logInfo("duplicate event detected: %s", eventID)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, `{"status":"duplicate","message":"event already processed"}`)
			return
		}
		s.trackEventID(eventID)
	}

	// Record stats
	s.stats.Record(payload.EventType, payload.Resource.Type)

	// Apply behavior mode
	switch s.cfg.Behavior.Mode {
	case "failure":
		s.respondFailure(w, eventID)
		return

	case "delay":
		time.Sleep(time.Duration(s.cfg.Behavior.DelayMs) * time.Millisecond)
		s.respondSuccess(w, eventID)
		return

	case "random":
		if rand.Float64() < s.cfg.Behavior.FailureRate {
			s.respondFailure(w, eventID)
			return
		}
		s.respondSuccess(w, eventID)
		return

	default: // "success"
		s.respondSuccess(w, eventID)
		return
	}
}

func (s *Server) respondSuccess(w http.ResponseWriter, eventID string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, `{"status":"accepted","event_id":"%s"}`, eventID)
}

func (s *Server) respondFailure(w http.ResponseWriter, eventID string) {
	statusCode := s.cfg.Behavior.StatusCode
	if statusCode == 0 {
		statusCode = http.StatusInternalServerError
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	fmt.Fprintf(w, `{"status":"error","event_id":"%s","message":"simulated failure"}`, eventID)
}

// isDuplicate checks whether the given event ID has already been seen.
func (s *Server) isDuplicate(eventID string) bool {
	s.seenMu.Lock()
	defer s.seenMu.Unlock()

	_, exists := s.seenIDs[eventID]
	return exists
}

// trackEventID records an event ID, evicting the oldest entry if the set is
// at capacity.
func (s *Server) trackEventID(eventID string) {
	s.seenMu.Lock()
	defer s.seenMu.Unlock()

	// Evict oldest if at capacity
	if len(s.seenList) >= s.cfg.Idempotency.MaxTracked {
		evict := s.seenList[0]
		s.seenList = s.seenList[1:]
		delete(s.seenIDs, evict)
	}

	s.seenIDs[eventID] = struct{}{}
	s.seenList = append(s.seenList, eventID)
}

func (s *Server) logInfo(format string, args ...interface{}) {
	if s.cfg.Logging.Format == "json" {
		msg := fmt.Sprintf(format, args...)
		log.Printf(`{"level":"info","msg":"%s","time":"%s"}`, msg, time.Now().Format(time.RFC3339))
	} else {
		log.Printf("[INFO] "+format, args...)
	}
}

func (s *Server) logError(format string, args ...interface{}) {
	if s.cfg.Logging.Format == "json" {
		msg := fmt.Sprintf(format, args...)
		log.Printf(`{"level":"error","msg":"%s","time":"%s"}`, msg, time.Now().Format(time.RFC3339))
	} else {
		log.Printf("[ERROR] "+format, args...)
	}
}
