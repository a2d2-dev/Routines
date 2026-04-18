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

// Package gateway implements the Routines Gateway: an HTTP server that accepts
// messages from schedule, webhook, and GitHub triggers, persists them in a
// POSIX file queue on a shared PVC, and serves a lease/ack/nack API for Agent
// Runtime pods to consume.
package gateway

import (
	"encoding/json"
	"time"
)

// Source identifies which trigger type produced a message.
type Source string

const (
	SourceSchedule Source = "schedule"
	SourceWebhook  Source = "webhook"
	SourceGitHub   Source = "github"
)

// Message is the canonical unit of work exchanged between the Gateway and
// Agent Runtime. It is serialised to JSON and stored as a single file in the
// file queue.
type Message struct {
	// DeliveryID is a globally-unique identifier for this delivery attempt.
	// Callers should generate a UUID; the Gateway preserves it.
	DeliveryID string `json:"deliveryID"`

	// RoutineUID is the Kubernetes UID of the target Routine CR.
	RoutineUID string `json:"routineUID"`

	// Source identifies the trigger type that produced this message.
	Source Source `json:"source"`

	// Payload is the raw trigger payload (webhook body, GitHub event JSON, etc.).
	// For schedule triggers it is typically empty ({}).
	Payload json.RawMessage `json:"payload,omitempty"`

	// EnqueuedAt is the UTC wall-clock time when the message was written to inbox.
	EnqueuedAt time.Time `json:"enqueuedAt"`

	// Metadata holds arbitrary string key/value pairs from the trigger
	// (e.g. "X-GitHub-Event", "webhookName").
	Metadata map[string]string `json:"metadata,omitempty"`

	// RetryCount is incremented each time a nack causes re-enqueue.
	RetryCount int `json:"retryCount,omitempty"`

	// LeasedAt is set by the Gateway when the message is moved to processing.
	LeasedAt *time.Time `json:"leasedAt,omitempty"`

	// LeaseExpiry is the deadline after which the reaper returns the message to inbox.
	LeaseExpiry *time.Time `json:"leaseExpiry,omitempty"`
}

// EventKind describes the type of an event entry in the audit JSONL.
type EventKind string

const (
	EventEnqueued  EventKind = "enqueued"
	EventLeased    EventKind = "leased"
	EventAcked     EventKind = "acked"
	EventNacked    EventKind = "nacked"
	EventExpired   EventKind = "expired"
	EventHeartbeat EventKind = "heartbeat"
)

// Event is one append-only entry in events/{routineUID}.jsonl.
type Event struct {
	Kind       EventKind         `json:"kind"`
	DeliveryID string            `json:"deliveryID"`
	RoutineUID string            `json:"routineUID"`
	Timestamp  time.Time         `json:"timestamp"`
	Metadata   map[string]string `json:"metadata,omitempty"`
}

// AckRequest is the body sent by the Agent Runtime on POST /v1/ack.
type AckRequest struct {
	DeliveryID string `json:"deliveryID"`
	RoutineUID string `json:"routineUID"`

	// ExitCode is the exit code of the Claude Code child process.
	ExitCode int `json:"exitCode,omitempty"`

	// DurationMs is the wall-clock duration of the run in milliseconds.
	DurationMs int64 `json:"durationMs,omitempty"`

	// TokensUsed is the total token count reported by Claude Code stdout.
	TokensUsed int64 `json:"tokensUsed,omitempty"`
}

// NackRequest is the body sent by the Agent Runtime on POST /v1/nack.
type NackRequest struct {
	DeliveryID string `json:"deliveryID"`
	RoutineUID string `json:"routineUID"`

	// Reason is a human-readable failure description.
	Reason string `json:"reason,omitempty"`

	// Retryable, when true, causes the Gateway to re-enqueue with incremented
	// RetryCount instead of moving to failed/.
	Retryable bool `json:"retryable,omitempty"`

	// BackoffSeconds hints how long to wait before making the message leasable
	// again (best-effort; Gateway may ignore it in MVP).
	BackoffSeconds int `json:"backoffSeconds,omitempty"`
}

// HeartbeatRequest is the body sent by the Agent Runtime on POST /v1/heartbeat/{routineUID}.
type HeartbeatRequest struct {
	RoutineUID       string `json:"routineUID"`
	CurrentMessageID string `json:"currentMessageID,omitempty"`

	// ExtendBySeconds requests that the lease expiry be pushed out by this many seconds.
	// Defaults to the server's default lease TTL when zero.
	ExtendBySeconds int `json:"extendBySeconds,omitempty"`
}
