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

package gateway_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/a2d2-dev/routines/internal/gateway"
)

func newTestServer(t *testing.T) *gateway.Server {
	t.Helper()
	root := t.TempDir()
	cfg := gateway.DefaultConfig()
	cfg.DataRoot = root
	cfg.LeaseTTL = 5 * time.Second
	cfg.WebhookSecrets = map[string]string{
		"test-hook": "supersecret",
	}
	cfg.GitHubWebhookSecret = "gh-secret"
	return gateway.NewServer(cfg)
}

// TestWebhookHMACValid verifies a correctly signed HMAC webhook is accepted.
func TestWebhookHMACValid(t *testing.T) {
	srv := newTestServer(t)

	body := []byte(`{"event":"push"}`)
	sig := gateway.ComputeHMACSHA256(body, "supersecret")

	req := httptest.NewRequest(http.MethodPost, "/webhooks/test-hook?routineUID=routine-wh", bytes.NewReader(body))
	req.Header.Set("X-Hub-Signature-256", sig)
	req.Header.Set("X-Delivery-ID", "delivery-wh-1")

	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)

	if rr.Code != http.StatusAccepted {
		t.Errorf("status: got %d, want %d; body: %s", rr.Code, http.StatusAccepted, rr.Body.String())
	}
}

// TestWebhookHMACInvalid verifies a bad signature is rejected.
func TestWebhookHMACInvalid(t *testing.T) {
	srv := newTestServer(t)

	body := []byte(`{"event":"push"}`)

	req := httptest.NewRequest(http.MethodPost, "/webhooks/test-hook?routineUID=routine-wh", bytes.NewReader(body))
	req.Header.Set("X-Hub-Signature-256", "sha256=deadbeef")
	req.Header.Set("X-Delivery-ID", "delivery-wh-bad")

	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("status: got %d, want %d", rr.Code, http.StatusUnauthorized)
	}
}

// TestWebhookBearerValid verifies an Authorization: Bearer token webhook is accepted.
func TestWebhookBearerValid(t *testing.T) {
	srv := newTestServer(t)

	body := []byte(`{"hello":"world"}`)

	req := httptest.NewRequest(http.MethodPost, "/webhooks/test-hook?routineUID=routine-bearer", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer supersecret")

	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)

	if rr.Code != http.StatusAccepted {
		t.Errorf("status: got %d, want %d; body: %s", rr.Code, http.StatusAccepted, rr.Body.String())
	}
}

// TestWebhookBearerInvalid verifies a wrong Bearer token is rejected.
func TestWebhookBearerInvalid(t *testing.T) {
	srv := newTestServer(t)

	body := []byte(`{"hello":"world"}`)

	req := httptest.NewRequest(http.MethodPost, "/webhooks/test-hook?routineUID=routine-bearer", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer wrong-secret")

	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("status: got %d, want %d", rr.Code, http.StatusUnauthorized)
	}
}

// TestWebhookNoSecret verifies webhooks without a configured secret are accepted without validation.
func TestWebhookNoSecret(t *testing.T) {
	srv := newTestServer(t)

	body := []byte(`{"data":"open"}`)

	req := httptest.NewRequest(http.MethodPost, "/webhooks/no-secret-hook?routineUID=routine-open", bytes.NewReader(body))

	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)

	if rr.Code != http.StatusAccepted {
		t.Errorf("status: got %d, want %d; body: %s", rr.Code, http.StatusAccepted, rr.Body.String())
	}
}

// TestGitHubWebhookValid verifies a correct GitHub webhook is accepted.
func TestGitHubWebhookValid(t *testing.T) {
	srv := newTestServer(t)

	body := []byte(`{"action":"opened"}`)
	sig := gateway.ComputeHMACSHA256(body, "gh-secret")

	req := httptest.NewRequest(http.MethodPost, "/webhooks/github/42?routineUID=routine-gh", bytes.NewReader(body))
	req.Header.Set("X-Hub-Signature-256", sig)
	req.Header.Set("X-GitHub-Event", "pull_request")
	req.Header.Set("X-GitHub-Delivery", "gh-delivery-1")

	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)

	if rr.Code != http.StatusAccepted {
		t.Errorf("status: got %d, want %d; body: %s", rr.Code, http.StatusAccepted, rr.Body.String())
	}
}

// TestGitHubWebhookInvalidSig verifies a bad GitHub signature is rejected.
func TestGitHubWebhookInvalidSig(t *testing.T) {
	srv := newTestServer(t)

	body := []byte(`{"action":"opened"}`)

	req := httptest.NewRequest(http.MethodPost, "/webhooks/github/42?routineUID=routine-gh", bytes.NewReader(body))
	req.Header.Set("X-Hub-Signature-256", "sha256=badfeed")
	req.Header.Set("X-GitHub-Event", "pull_request")

	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("status: got %d, want %d", rr.Code, http.StatusUnauthorized)
	}
}

// TestEnqueueLeaseAckIntegration is an end-to-end test via HTTP.
func TestEnqueueLeaseAckIntegration(t *testing.T) {
	srv := newTestServer(t)

	// Enqueue.
	msg := gateway.Message{
		DeliveryID: "int-delivery-1",
		RoutineUID: "int-routine",
		Source:     gateway.SourceSchedule,
		Payload:    json.RawMessage(`{}`),
	}
	body, _ := json.Marshal(msg)
	req := httptest.NewRequest(http.MethodPost, "/v1/enqueue", bytes.NewReader(body))
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("enqueue: %d %s", rr.Code, rr.Body.String())
	}

	// Lease (no wait — message already in inbox).
	req2 := httptest.NewRequest(http.MethodGet, "/v1/lease/int-routine?wait=0s", nil)
	rr2 := httptest.NewRecorder()
	srv.ServeHTTP(rr2, req2)
	if rr2.Code != http.StatusOK {
		t.Fatalf("lease: %d %s", rr2.Code, rr2.Body.String())
	}

	var leased gateway.Message
	if err := json.NewDecoder(rr2.Body).Decode(&leased); err != nil {
		t.Fatalf("decode leased: %v", err)
	}
	if leased.DeliveryID != "int-delivery-1" {
		t.Errorf("leased deliveryID: %q", leased.DeliveryID)
	}

	// Ack.
	ackBody, _ := json.Marshal(gateway.AckRequest{
		DeliveryID: "int-delivery-1",
		RoutineUID: "int-routine",
		ExitCode:   0,
		DurationMs: 1234,
	})
	req3 := httptest.NewRequest(http.MethodPost, "/v1/ack", bytes.NewReader(ackBody))
	rr3 := httptest.NewRecorder()
	srv.ServeHTTP(rr3, req3)
	if rr3.Code != http.StatusNoContent {
		t.Fatalf("ack: %d %s", rr3.Code, rr3.Body.String())
	}

	// Lease again — should be empty.
	req4 := httptest.NewRequest(http.MethodGet, "/v1/lease/int-routine?wait=0s", nil)
	rr4 := httptest.NewRecorder()
	srv.ServeHTTP(rr4, req4)
	if rr4.Code != http.StatusNoContent {
		t.Fatalf("expected 204 after ack, got %d %s", rr4.Code, rr4.Body.String())
	}
}

// TestHistoryEndpoint verifies /v1/history returns events.
func TestHistoryEndpoint(t *testing.T) {
	srv := newTestServer(t)

	msg := gateway.Message{
		DeliveryID: "hist-1",
		RoutineUID: "hist-routine",
		Source:     gateway.SourceSchedule,
		Payload:    json.RawMessage(`{}`),
	}
	body, _ := json.Marshal(msg)
	req := httptest.NewRequest(http.MethodPost, "/v1/enqueue", bytes.NewReader(body))
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)

	req2 := httptest.NewRequest(http.MethodGet, "/v1/history/hist-routine", nil)
	rr2 := httptest.NewRecorder()
	srv.ServeHTTP(rr2, req2)
	if rr2.Code != http.StatusOK {
		t.Fatalf("history: %d %s", rr2.Code, rr2.Body.String())
	}

	var events []gateway.Event
	if err := json.NewDecoder(rr2.Body).Decode(&events); err != nil {
		t.Fatalf("decode events: %v", err)
	}
	if len(events) == 0 {
		t.Error("expected at least one event")
	}
}
