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
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/google/uuid"
)

const maxWebhookBodyBytes = 8 * 1024 * 1024 // 8 MiB

// ------------------------------------------------------------------
// POST /webhooks/{name}
// Accepts an external webhook, validates the signature (HMAC-SHA256
// or Bearer token), and enqueues a Message.
// ------------------------------------------------------------------

func (s *Server) handleWebhook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Path: /webhooks/{name}[/{routineUID}]
	// We require routineUID either as query param or as second path segment.
	path := strings.TrimPrefix(r.URL.Path, "/webhooks/")
	parts := strings.SplitN(path, "/", 2)
	webhookName := parts[0]
	if webhookName == "" {
		http.Error(w, "webhook name required", http.StatusBadRequest)
		return
	}

	routineUID := r.URL.Query().Get("routineUID")
	if routineUID == "" && len(parts) > 1 {
		routineUID = parts[1]
	}
	if routineUID == "" {
		http.Error(w, "routineUID required (query param or path segment)", http.StatusBadRequest)
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, maxWebhookBodyBytes))
	if err != nil {
		http.Error(w, "read body", http.StatusBadRequest)
		return
	}

	// Validate signature if a secret is configured for this webhook.
	if secret, ok := s.cfg.WebhookSecrets[webhookName]; ok && secret != "" {
		if err := validateWebhookSignature(r, body, secret); err != nil {
			http.Error(w, fmt.Sprintf("signature validation: %v", err), http.StatusUnauthorized)
			return
		}
	}

	deliveryID := r.Header.Get("X-Delivery-ID")
	if deliveryID == "" {
		deliveryID = uuid.NewString()
	}

	msg := &Message{
		DeliveryID: deliveryID,
		RoutineUID: routineUID,
		Source:     SourceWebhook,
		Payload:    json.RawMessage(body),
		Metadata: map[string]string{
			"webhookName":  webhookName,
			"content-type": r.Header.Get("Content-Type"),
		},
	}

	if err := s.queue.Enqueue(msg); err != nil {
		http.Error(w, fmt.Sprintf("enqueue: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(map[string]string{"deliveryID": deliveryID})
}

// ------------------------------------------------------------------
// POST /webhooks/github/{installation}
// Validates the GitHub webhook signature (X-Hub-Signature-256) and
// enqueues the event.
// ------------------------------------------------------------------

func (s *Server) handleGitHubWebhook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	path := strings.TrimPrefix(r.URL.Path, "/webhooks/github/")
	parts := strings.SplitN(path, "/", 2)
	installation := parts[0]
	if installation == "" {
		http.Error(w, "installation required in path", http.StatusBadRequest)
		return
	}

	routineUID := r.URL.Query().Get("routineUID")
	if routineUID == "" {
		http.Error(w, "routineUID required as query param", http.StatusBadRequest)
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, maxWebhookBodyBytes))
	if err != nil {
		http.Error(w, "read body", http.StatusBadRequest)
		return
	}

	if s.cfg.GitHubWebhookSecret != "" {
		if err := validateGitHubSignature(r, body, s.cfg.GitHubWebhookSecret); err != nil {
			http.Error(w, fmt.Sprintf("github signature: %v", err), http.StatusUnauthorized)
			return
		}
	}

	deliveryID := r.Header.Get("X-GitHub-Delivery")
	if deliveryID == "" {
		deliveryID = uuid.NewString()
	}
	eventType := r.Header.Get("X-GitHub-Event")

	msg := &Message{
		DeliveryID: deliveryID,
		RoutineUID: routineUID,
		Source:     SourceGitHub,
		Payload:    json.RawMessage(body),
		Metadata: map[string]string{
			"githubEvent":      eventType,
			"installation":     installation,
			"x-github-hook-id": r.Header.Get("X-GitHub-Hook-ID"),
		},
	}

	if err := s.queue.Enqueue(msg); err != nil {
		http.Error(w, fmt.Sprintf("enqueue: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(map[string]string{"deliveryID": deliveryID})
}

// ------------------------------------------------------------------
// Signature helpers
// ------------------------------------------------------------------

// validateWebhookSignature checks either:
//   - X-Hub-Signature-256: sha256=<hex>  (HMAC-SHA256)
//   - Authorization: Bearer <secret>
func validateWebhookSignature(r *http.Request, body []byte, secret string) error {
	// Try HMAC-SHA256 first.
	if sig := r.Header.Get("X-Hub-Signature-256"); sig != "" {
		return validateHMACSHA256(sig, body, secret)
	}
	// Try Bearer token.
	if auth := r.Header.Get("Authorization"); auth != "" {
		token := strings.TrimPrefix(auth, "Bearer ")
		if !hmac.Equal([]byte(token), []byte(secret)) {
			return fmt.Errorf("invalid bearer token")
		}
		return nil
	}
	return fmt.Errorf("no signature header present (X-Hub-Signature-256 or Authorization)")
}

// validateGitHubSignature validates X-Hub-Signature-256 as required by GitHub Apps.
func validateGitHubSignature(r *http.Request, body []byte, secret string) error {
	sig := r.Header.Get("X-Hub-Signature-256")
	if sig == "" {
		return fmt.Errorf("missing X-Hub-Signature-256")
	}
	return validateHMACSHA256(sig, body, secret)
}

// validateHMACSHA256 checks that sigHeader (format "sha256=<hex>") matches
// the HMAC-SHA256 of body with secret.
func validateHMACSHA256(sigHeader string, body []byte, secret string) error {
	const prefix = "sha256="
	if !strings.HasPrefix(sigHeader, prefix) {
		return fmt.Errorf("signature must start with sha256=")
	}
	got, err := hex.DecodeString(strings.TrimPrefix(sigHeader, prefix))
	if err != nil {
		return fmt.Errorf("decode signature hex: %w", err)
	}

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	want := mac.Sum(nil)

	if !hmac.Equal(got, want) {
		return fmt.Errorf("signature mismatch")
	}
	return nil
}

// ComputeHMACSHA256 returns the sha256=<hex> signature for body using secret.
// Exported for use in tests.
func ComputeHMACSHA256(body []byte, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}
