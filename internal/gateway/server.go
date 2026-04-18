/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Config holds runtime configuration for the Gateway server.
type Config struct {
	// DataRoot is the path to the PVC mount point (e.g. /data).
	DataRoot string

	// ListenAddr is the TCP address to listen on (e.g. :8080).
	ListenAddr string

	// LeaseTTL is the maximum time a leased message stays in processing/
	// before the reaper returns it to inbox/.
	LeaseTTL time.Duration

	// ReaperInterval is how often the reaper goroutine runs.
	ReaperInterval time.Duration

	// DefaultWait is the default long-poll wait duration for /v1/lease.
	DefaultWait time.Duration

	// MaxWait is the maximum long-poll wait duration for /v1/lease.
	MaxWait time.Duration

	// WebhookSecrets maps webhookName → HMAC-SHA256 secret (hex or base64).
	WebhookSecrets map[string]string

	// GitHubWebhookSecret is the shared secret for GitHub App webhook validation.
	GitHubWebhookSecret string
}

// DefaultConfig returns a Config with sensible defaults.
func DefaultConfig() Config {
	return Config{
		DataRoot:       "/data",
		ListenAddr:     ":8080",
		LeaseTTL:       DefaultLeaseTTL,
		ReaperInterval: DefaultReaperInterval,
		DefaultWait:    30 * time.Second,
		MaxWait:        2 * time.Minute,
	}
}

// Server is the Gateway HTTP server.
type Server struct {
	cfg     Config
	queue   *FileQueue
	mux     *http.ServeMux
	httpSrv *http.Server
}

// NewServer creates a Server with the given Config.
func NewServer(cfg Config) *Server {
	s := &Server{
		cfg:   cfg,
		queue: NewFileQueue(cfg.DataRoot, cfg.LeaseTTL),
		mux:   http.NewServeMux(),
	}
	s.registerRoutes()
	s.httpSrv = &http.Server{
		Addr:    cfg.ListenAddr,
		Handler: s.mux,
	}
	return s
}

// Start begins listening and starts background goroutines (reaper).
// It blocks until ctx is cancelled or a fatal listen error occurs.
func (s *Server) Start(ctx context.Context) error {
	go s.queue.RunReaper(ctx, s.cfg.ReaperInterval)

	errCh := make(chan error, 1)
	go func() {
		if err := s.httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return s.httpSrv.Shutdown(shutdownCtx)
	case err := <-errCh:
		return err
	}
}

// ServeHTTP implements http.Handler, delegating to the internal mux.
// This makes Server usable with httptest.NewRecorder in tests.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

// registerRoutes wires up all HTTP routes.
func (s *Server) registerRoutes() {
	// Health probes
	s.mux.HandleFunc("/healthz", s.handleHealthz)
	s.mux.HandleFunc("/readyz", s.handleReadyz)

	// Internal API
	s.mux.HandleFunc("/v1/enqueue", s.handleEnqueue)
	s.mux.HandleFunc("/v1/lease/", s.handleLease)
	s.mux.HandleFunc("/v1/ack", s.handleAck)
	s.mux.HandleFunc("/v1/nack", s.handleNack)
	s.mux.HandleFunc("/v1/heartbeat/", s.handleHeartbeat)
	s.mux.HandleFunc("/v1/history/", s.handleHistory)

	// Webhook ingress
	s.mux.HandleFunc("/webhooks/github/", s.handleGitHubWebhook)
	s.mux.HandleFunc("/webhooks/", s.handleWebhook)
}

// ------------------------------------------------------------------
// Health probes
// ------------------------------------------------------------------

func (s *Server) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

func (s *Server) handleReadyz(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

// ------------------------------------------------------------------
// POST /v1/enqueue
// ------------------------------------------------------------------

func (s *Server) handleEnqueue(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var msg Message
	if err := json.NewDecoder(r.Body).Decode(&msg); err != nil {
		http.Error(w, fmt.Sprintf("decode body: %v", err), http.StatusBadRequest)
		return
	}
	if msg.DeliveryID == "" || msg.RoutineUID == "" {
		http.Error(w, "deliveryID and routineUID are required", http.StatusBadRequest)
		return
	}

	if err := s.queue.Enqueue(&msg); err != nil {
		http.Error(w, fmt.Sprintf("enqueue: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(map[string]string{"deliveryID": msg.DeliveryID})
}

// ------------------------------------------------------------------
// GET /v1/lease/{routineUID}[?wait=30s]
// ------------------------------------------------------------------

func (s *Server) handleLease(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	routineUID := strings.TrimPrefix(r.URL.Path, "/v1/lease/")
	routineUID = strings.Trim(routineUID, "/")
	if routineUID == "" {
		http.Error(w, "routineUID required in path", http.StatusBadRequest)
		return
	}

	wait := s.cfg.DefaultWait
	if wq := r.URL.Query().Get("wait"); wq != "" {
		d, err := time.ParseDuration(wq)
		if err != nil {
			http.Error(w, fmt.Sprintf("invalid wait duration: %v", err), http.StatusBadRequest)
			return
		}
		if d > s.cfg.MaxWait {
			d = s.cfg.MaxWait
		}
		wait = d
	}

	msg, err := s.queue.LeasePoll(r.Context(), routineUID, wait)
	if err != nil {
		if err == context.Canceled || err == context.DeadlineExceeded {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		http.Error(w, fmt.Sprintf("lease: %v", err), http.StatusInternalServerError)
		return
	}
	if msg == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(msg)
}

// ------------------------------------------------------------------
// POST /v1/ack
// ------------------------------------------------------------------

func (s *Server) handleAck(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req AckRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, fmt.Sprintf("decode body: %v", err), http.StatusBadRequest)
		return
	}
	if req.DeliveryID == "" || req.RoutineUID == "" {
		http.Error(w, "deliveryID and routineUID are required", http.StatusBadRequest)
		return
	}

	meta := map[string]string{
		"exitCode":   strconv.Itoa(req.ExitCode),
		"durationMs": strconv.FormatInt(req.DurationMs, 10),
		"tokensUsed": strconv.FormatInt(req.TokensUsed, 10),
	}
	if err := s.queue.Ack(req.RoutineUID, req.DeliveryID, meta); err != nil {
		http.Error(w, fmt.Sprintf("ack: %v", err), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// ------------------------------------------------------------------
// POST /v1/nack
// ------------------------------------------------------------------

func (s *Server) handleNack(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req NackRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, fmt.Sprintf("decode body: %v", err), http.StatusBadRequest)
		return
	}
	if req.DeliveryID == "" || req.RoutineUID == "" {
		http.Error(w, "deliveryID and routineUID are required", http.StatusBadRequest)
		return
	}

	meta := map[string]string{"reason": req.Reason}
	if err := s.queue.Nack(req.RoutineUID, req.DeliveryID, req.Retryable, meta); err != nil {
		http.Error(w, fmt.Sprintf("nack: %v", err), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// ------------------------------------------------------------------
// POST /v1/heartbeat/{routineUID}
// ------------------------------------------------------------------

func (s *Server) handleHeartbeat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	routineUID := strings.TrimPrefix(r.URL.Path, "/v1/heartbeat/")
	routineUID = strings.Trim(routineUID, "/")
	if routineUID == "" {
		http.Error(w, "routineUID required in path", http.StatusBadRequest)
		return
	}

	var req HeartbeatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, fmt.Sprintf("decode body: %v", err), http.StatusBadRequest)
		return
	}

	extendBy := s.cfg.LeaseTTL
	if req.ExtendBySeconds > 0 {
		extendBy = time.Duration(req.ExtendBySeconds) * time.Second
	}

	if req.CurrentMessageID != "" {
		if err := s.queue.ExtendLease(routineUID, req.CurrentMessageID, extendBy); err != nil {
			// Non-fatal: message may have been acked or the lease already expired.
			// Return 200 anyway so the runtime doesn't panic.
			_, _ = fmt.Fprintf(w, `{"warning":"extend lease: %v"}`, err)
			return
		}
	}

	if err := s.queue.appendEvent(routineUID, EventHeartbeat, req.CurrentMessageID, nil); err != nil {
		_, _ = fmt.Fprintf(w, `{"warning":"append event: %v"}`, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// ------------------------------------------------------------------
// GET /v1/history/{routineUID}[?since=<RFC3339>]
// ------------------------------------------------------------------

func (s *Server) handleHistory(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	routineUID := strings.TrimPrefix(r.URL.Path, "/v1/history/")
	routineUID = strings.Trim(routineUID, "/")
	if routineUID == "" {
		http.Error(w, "routineUID required in path", http.StatusBadRequest)
		return
	}

	var since time.Time
	if sq := r.URL.Query().Get("since"); sq != "" {
		t, err := time.Parse(time.RFC3339, sq)
		if err != nil {
			http.Error(w, fmt.Sprintf("invalid since timestamp: %v", err), http.StatusBadRequest)
			return
		}
		since = t
	}

	events, err := s.queue.History(routineUID, since)
	if err != nil {
		http.Error(w, fmt.Sprintf("history: %v", err), http.StatusInternalServerError)
		return
	}
	if events == nil {
		events = []Event{}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(events)
}
