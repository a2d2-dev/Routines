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

// TestHealthProbes verifies /healthz and /readyz return 200.
func TestHealthProbes(t *testing.T) {
	srv := newTestServer(t)

	for _, path := range []string{"/healthz", "/readyz"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rr := httptest.NewRecorder()
		srv.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Errorf("%s: got %d, want 200", path, rr.Code)
		}
	}
}

// TestEnqueueMissingFields verifies 400 when deliveryID or routineUID is absent.
func TestEnqueueMissingFields(t *testing.T) {
	srv := newTestServer(t)

	for _, body := range []string{
		`{"routineUID":"r"}`, // missing deliveryID
		`{"deliveryID":"d"}`, // missing routineUID
		`{}`,                 // both missing
	} {
		req := httptest.NewRequest(http.MethodPost, "/v1/enqueue", bytes.NewBufferString(body))
		rr := httptest.NewRecorder()
		srv.ServeHTTP(rr, req)
		if rr.Code != http.StatusBadRequest {
			t.Errorf("body %q: got %d, want 400", body, rr.Code)
		}
	}
}

// TestEnqueueBadJSON verifies 400 on malformed JSON.
func TestEnqueueBadJSON(t *testing.T) {
	srv := newTestServer(t)

	req := httptest.NewRequest(http.MethodPost, "/v1/enqueue", bytes.NewBufferString("not-json"))
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("got %d, want 400", rr.Code)
	}
}

// TestEnqueueWrongMethod verifies 405 on non-POST.
func TestEnqueueWrongMethod(t *testing.T) {
	srv := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/v1/enqueue", nil)
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("got %d, want 405", rr.Code)
	}
}

// TestLeaseNoRoutineUID verifies 400 when path has no routineUID.
func TestLeaseNoRoutineUID(t *testing.T) {
	srv := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/v1/lease/", nil)
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("got %d, want 400", rr.Code)
	}
}

// TestLeaseBadWaitParam verifies 400 on invalid ?wait= value.
func TestLeaseBadWaitParam(t *testing.T) {
	srv := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/v1/lease/routine-x?wait=notaduration", nil)
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("got %d, want 400", rr.Code)
	}
}

// TestLeaseWrongMethod verifies 405 on non-GET.
func TestLeaseWrongMethod(t *testing.T) {
	srv := newTestServer(t)

	req := httptest.NewRequest(http.MethodPost, "/v1/lease/routine-x", nil)
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("got %d, want 405", rr.Code)
	}
}

// TestAckMissingFields verifies 400 when fields are absent.
func TestAckMissingFields(t *testing.T) {
	srv := newTestServer(t)

	body, _ := json.Marshal(gateway.AckRequest{DeliveryID: "d"}) // missing routineUID
	req := httptest.NewRequest(http.MethodPost, "/v1/ack", bytes.NewReader(body))
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("got %d, want 400", rr.Code)
	}
}

// TestNackEnqueueLease verifies nack flow via HTTP.
func TestNackEnqueueLease(t *testing.T) {
	srv := newTestServer(t)

	// Enqueue.
	enqMsg := gateway.Message{
		DeliveryID: "nack-http-1",
		RoutineUID: "nack-routine",
		Source:     gateway.SourceSchedule,
		Payload:    json.RawMessage(`{}`),
	}
	b, _ := json.Marshal(enqMsg)
	req := httptest.NewRequest(http.MethodPost, "/v1/enqueue", bytes.NewReader(b))
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("enqueue: %d", rr.Code)
	}

	// Lease.
	req2 := httptest.NewRequest(http.MethodGet, "/v1/lease/nack-routine?wait=0s", nil)
	rr2 := httptest.NewRecorder()
	srv.ServeHTTP(rr2, req2)
	if rr2.Code != http.StatusOK {
		t.Fatalf("lease: %d %s", rr2.Code, rr2.Body.String())
	}

	// Nack retryable=false.
	nackBody, _ := json.Marshal(gateway.NackRequest{
		DeliveryID: "nack-http-1",
		RoutineUID: "nack-routine",
		Reason:     "test failure",
		Retryable:  false,
	})
	req3 := httptest.NewRequest(http.MethodPost, "/v1/nack", bytes.NewReader(nackBody))
	rr3 := httptest.NewRecorder()
	srv.ServeHTTP(rr3, req3)
	if rr3.Code != http.StatusNoContent {
		t.Fatalf("nack: %d %s", rr3.Code, rr3.Body.String())
	}
}

// TestNackMissingFields verifies 400 when fields are absent.
func TestNackMissingFields(t *testing.T) {
	srv := newTestServer(t)

	body, _ := json.Marshal(gateway.NackRequest{RoutineUID: "r"}) // missing deliveryID
	req := httptest.NewRequest(http.MethodPost, "/v1/nack", bytes.NewReader(body))
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("got %d, want 400", rr.Code)
	}
}

// TestHeartbeatEndpoint verifies POST /v1/heartbeat/{routineUID} returns 204.
func TestHeartbeatEndpoint(t *testing.T) {
	srv := newTestServer(t)

	// Enqueue and lease a message so heartbeat has something to extend.
	enqMsg := gateway.Message{
		DeliveryID: "hb-delivery-1",
		RoutineUID: "hb-routine",
		Source:     gateway.SourceSchedule,
		Payload:    json.RawMessage(`{}`),
	}
	b, _ := json.Marshal(enqMsg)
	req := httptest.NewRequest(http.MethodPost, "/v1/enqueue", bytes.NewReader(b))
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)

	req2 := httptest.NewRequest(http.MethodGet, "/v1/lease/hb-routine?wait=0s", nil)
	rr2 := httptest.NewRecorder()
	srv.ServeHTTP(rr2, req2)

	hbBody, _ := json.Marshal(gateway.HeartbeatRequest{
		RoutineUID:       "hb-routine",
		CurrentMessageID: "hb-delivery-1",
		ExtendBySeconds:  120,
	})
	req3 := httptest.NewRequest(http.MethodPost, "/v1/heartbeat/hb-routine", bytes.NewReader(hbBody))
	rr3 := httptest.NewRecorder()
	srv.ServeHTTP(rr3, req3)
	if rr3.Code != http.StatusNoContent {
		t.Errorf("heartbeat: got %d, want 204; body: %s", rr3.Code, rr3.Body.String())
	}
}

// TestHeartbeatWrongMethod verifies 405 on non-POST.
func TestHeartbeatWrongMethod(t *testing.T) {
	srv := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/v1/heartbeat/routine-x", nil)
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("got %d, want 405", rr.Code)
	}
}

// TestHistoryWithSince verifies ?since= filtering works.
func TestHistoryWithSince(t *testing.T) {
	srv := newTestServer(t)

	msg := gateway.Message{
		DeliveryID: "since-1",
		RoutineUID: "since-routine",
		Source:     gateway.SourceSchedule,
		Payload:    json.RawMessage(`{}`),
	}
	b, _ := json.Marshal(msg)
	req := httptest.NewRequest(http.MethodPost, "/v1/enqueue", bytes.NewReader(b))
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)

	// Query with since = far future — should return nothing.
	future := time.Now().Add(1 * time.Hour).UTC().Format(time.RFC3339)
	req2 := httptest.NewRequest(http.MethodGet, "/v1/history/since-routine?since="+future, nil)
	rr2 := httptest.NewRecorder()
	srv.ServeHTTP(rr2, req2)
	if rr2.Code != http.StatusOK {
		t.Fatalf("history: %d %s", rr2.Code, rr2.Body.String())
	}

	var events []gateway.Event
	if err := json.NewDecoder(rr2.Body).Decode(&events); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(events) != 0 {
		t.Errorf("expected 0 events with future since, got %d", len(events))
	}
}

// TestHistoryBadSince verifies 400 on invalid ?since= value.
func TestHistoryBadSince(t *testing.T) {
	srv := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/v1/history/routine-x?since=notadate", nil)
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("got %d, want 400", rr.Code)
	}
}

// TestWebhookMissingRoutineUID verifies 400 when routineUID is absent.
func TestWebhookMissingRoutineUID(t *testing.T) {
	srv := newTestServer(t)

	req := httptest.NewRequest(http.MethodPost, "/webhooks/test-hook", bytes.NewBufferString(`{}`))
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("got %d, want 400", rr.Code)
	}
}

// TestGitHubWebhookMissingRoutineUID verifies 400 when routineUID is absent.
func TestGitHubWebhookMissingRoutineUID(t *testing.T) {
	srv := newTestServer(t)

	req := httptest.NewRequest(http.MethodPost, "/webhooks/github/42", bytes.NewBufferString(`{}`))
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("got %d, want 400", rr.Code)
	}
}
