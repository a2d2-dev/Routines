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

package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/a2d2-dev/routines/internal/gateway"
)

func TestGatewayClient_Lease_NoContent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	client := NewGatewayClient(srv.URL, 5*time.Second)
	msg, err := client.Lease(context.Background(), "uid-1", 1*time.Second)
	if err != nil {
		t.Fatalf("Lease() error = %v", err)
	}
	if msg != nil {
		t.Fatalf("Lease() = %v, want nil", msg)
	}
}

func TestGatewayClient_Lease_Message(t *testing.T) {
	want := &gateway.Message{
		DeliveryID: "del-1",
		RoutineUID: "uid-1",
		Source:     gateway.SourceSchedule,
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(want)
	}))
	defer srv.Close()

	client := NewGatewayClient(srv.URL, 5*time.Second)
	got, err := client.Lease(context.Background(), "uid-1", 1*time.Second)
	if err != nil {
		t.Fatalf("Lease() error = %v", err)
	}
	if got == nil {
		t.Fatal("Lease() = nil, want message")
	}
	if got.DeliveryID != want.DeliveryID {
		t.Errorf("DeliveryID = %q, want %q", got.DeliveryID, want.DeliveryID)
	}
}

func TestGatewayClient_Ack(t *testing.T) {
	var gotBody map[string]interface{}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/ack" || r.Method != http.MethodPost {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	client := NewGatewayClient(srv.URL, 5*time.Second)
	err := client.Ack(context.Background(), "uid-1", "del-1", 0, 1234, 500)
	if err != nil {
		t.Fatalf("Ack() error = %v", err)
	}

	if gotBody["deliveryID"] != "del-1" {
		t.Errorf("deliveryID = %v, want del-1", gotBody["deliveryID"])
	}
	if gotBody["routineUID"] != "uid-1" {
		t.Errorf("routineUID = %v, want uid-1", gotBody["routineUID"])
	}
}

func TestGatewayClient_Nack(t *testing.T) {
	var gotBody map[string]interface{}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/nack" || r.Method != http.MethodPost {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	client := NewGatewayClient(srv.URL, 5*time.Second)
	err := client.Nack(context.Background(), "uid-1", "del-1", "something went wrong", false)
	if err != nil {
		t.Fatalf("Nack() error = %v", err)
	}

	if gotBody["reason"] != "something went wrong" {
		t.Errorf("reason = %v, want 'something went wrong'", gotBody["reason"])
	}
}

func TestGatewayClient_Heartbeat(t *testing.T) {
	called := false

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			called = true
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	client := NewGatewayClient(srv.URL, 5*time.Second)
	err := client.Heartbeat(context.Background(), "uid-1", "del-1", 60)
	if err != nil {
		t.Fatalf("Heartbeat() error = %v", err)
	}
	if !called {
		t.Error("expected server to be called")
	}
}
