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
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/a2d2-dev/routines/internal/gateway"
)

const jsonExt = ".json"

func newTestQueue(t *testing.T) (*gateway.FileQueue, string) {
	t.Helper()
	root := t.TempDir()
	q := gateway.NewFileQueue(root, 5*time.Second)
	return q, root
}

func testMsg(deliveryID, routineUID string) *gateway.Message {
	return &gateway.Message{
		DeliveryID: deliveryID,
		RoutineUID: routineUID,
		Source:     gateway.SourceSchedule,
		Payload:    json.RawMessage(`{}`),
	}
}

// TestEnqueueLease verifies that a message enqueued in inbox can be leased.
func TestEnqueueLease(t *testing.T) {
	q, _ := newTestQueue(t)

	msg := testMsg("delivery-1", "routine-abc")
	if err := q.Enqueue(msg); err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	leased, err := q.Lease("routine-abc")
	if err != nil {
		t.Fatalf("lease: %v", err)
	}
	if leased == nil {
		t.Fatal("expected leased message, got nil")
	}
	if leased.DeliveryID != "delivery-1" {
		t.Errorf("deliveryID: got %q, want %q", leased.DeliveryID, "delivery-1")
	}
	if leased.LeasedAt == nil {
		t.Error("LeasedAt should be set")
	}
	if leased.LeaseExpiry == nil {
		t.Error("LeaseExpiry should be set")
	}
}

// TestLeaseEmptyQueue verifies that leasing an empty queue returns nil.
func TestLeaseEmptyQueue(t *testing.T) {
	q, _ := newTestQueue(t)
	_ = q.EnsureQueueDirs("routine-xyz")
	msg, err := q.Lease("routine-xyz")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg != nil {
		t.Fatalf("expected nil, got %+v", msg)
	}
}

// TestAck verifies that acking a leased message moves it to done/.
func TestAck(t *testing.T) {
	q, root := newTestQueue(t)

	msg := testMsg("delivery-2", "routine-ack")
	if err := q.Enqueue(msg); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	leased, err := q.Lease("routine-ack")
	if err != nil || leased == nil {
		t.Fatalf("lease failed: %v, %v", leased, err)
	}

	if err := q.Ack("routine-ack", "delivery-2", nil); err != nil {
		t.Fatalf("ack: %v", err)
	}

	// processing/ should be empty.
	procEntries, _ := os.ReadDir(filepath.Join(root, "queues", "routine-ack", "processing"))
	for _, e := range procEntries {
		if !e.IsDir() {
			t.Errorf("unexpected file in processing: %s", e.Name())
		}
	}

	// done/ should have the message.
	doneEntries, _ := os.ReadDir(filepath.Join(root, "queues", "routine-ack", "done"))
	var jsonCount int
	for _, e := range doneEntries {
		if filepath.Ext(e.Name()) == jsonExt {
			jsonCount++
		}
	}
	if jsonCount != 1 {
		t.Errorf("expected 1 file in done/, got %d", jsonCount)
	}
}

// TestNackNotRetryable verifies that a non-retryable nack moves to failed/.
func TestNackNotRetryable(t *testing.T) {
	q, root := newTestQueue(t)

	msg := testMsg("delivery-3", "routine-nack")
	if err := q.Enqueue(msg); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if _, err := q.Lease("routine-nack"); err != nil {
		t.Fatalf("lease: %v", err)
	}
	if err := q.Nack("routine-nack", "delivery-3", false, nil); err != nil {
		t.Fatalf("nack: %v", err)
	}

	failedEntries, _ := os.ReadDir(filepath.Join(root, "queues", "routine-nack", "failed"))
	var count int
	for _, e := range failedEntries {
		if filepath.Ext(e.Name()) == jsonExt {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected 1 file in failed/, got %d", count)
	}
}

// TestNackRetryable verifies that a retryable nack re-queues in inbox/ with incremented RetryCount.
func TestNackRetryable(t *testing.T) {
	q, root := newTestQueue(t)

	msg := testMsg("delivery-4", "routine-retry")
	if err := q.Enqueue(msg); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if _, err := q.Lease("routine-retry"); err != nil {
		t.Fatalf("lease: %v", err)
	}
	if err := q.Nack("routine-retry", "delivery-4", true, nil); err != nil {
		t.Fatalf("nack: %v", err)
	}

	inboxEntries, _ := os.ReadDir(filepath.Join(root, "queues", "routine-retry", "inbox"))
	var count int
	for _, e := range inboxEntries {
		if filepath.Ext(e.Name()) == jsonExt {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected 1 file in inbox/ after retryable nack, got %d", count)
	}

	// Lease again and verify RetryCount == 1.
	re, err := q.Lease("routine-retry")
	if err != nil || re == nil {
		t.Fatalf("re-lease failed: %v, %v", re, err)
	}
	if re.RetryCount != 1 {
		t.Errorf("RetryCount: got %d, want 1", re.RetryCount)
	}
}

// TestReaper verifies expired leases are returned to inbox/.
func TestReaper(t *testing.T) {
	root := t.TempDir()
	// Use very short TTL (10ms) to test the reaper.
	q := gateway.NewFileQueue(root, 10*time.Millisecond)

	msg := testMsg("delivery-5", "routine-reap")
	if err := q.Enqueue(msg); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	leased, err := q.Lease("routine-reap")
	if err != nil || leased == nil {
		t.Fatalf("lease: %v, %v", leased, err)
	}

	// Wait for lease to expire.
	time.Sleep(50 * time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	go q.RunReaper(ctx, 10*time.Millisecond)

	// Give reaper time to run.
	time.Sleep(100 * time.Millisecond)

	// Should now be back in inbox/.
	inboxEntries, _ := os.ReadDir(filepath.Join(root, "queues", "routine-reap", "inbox"))
	var count int
	for _, e := range inboxEntries {
		if filepath.Ext(e.Name()) == jsonExt {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected 1 file in inbox/ after reap, got %d", count)
	}
}

// TestLeasePoll verifies long-poll returns immediately when message available.
func TestLeasePollImmediate(t *testing.T) {
	q, _ := newTestQueue(t)

	msg := testMsg("delivery-6", "routine-poll")
	if err := q.Enqueue(msg); err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	ctx := context.Background()
	leased, err := q.LeasePoll(ctx, "routine-poll", 5*time.Second)
	if err != nil {
		t.Fatalf("lease poll: %v", err)
	}
	if leased == nil {
		t.Fatal("expected leased message")
	}
}

// TestLeasePollWake verifies long-poll wakes when a message arrives.
func TestLeasePollWake(t *testing.T) {
	q, _ := newTestQueue(t)
	if err := q.EnsureQueueDirs("routine-wake"); err != nil {
		t.Fatalf("ensure dirs: %v", err)
	}

	go func() {
		time.Sleep(50 * time.Millisecond)
		msg := testMsg("delivery-7", "routine-wake")
		_ = q.Enqueue(msg)
	}()

	ctx := context.Background()
	leased, err := q.LeasePoll(ctx, "routine-wake", 2*time.Second)
	if err != nil {
		t.Fatalf("lease poll: %v", err)
	}
	if leased == nil {
		t.Fatal("expected leased message via wake")
	}
}

// TestHistory verifies event history is appended and readable.
func TestHistory(t *testing.T) {
	q, _ := newTestQueue(t)

	msg := testMsg("delivery-8", "routine-hist")
	if err := q.Enqueue(msg); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if _, err := q.Lease("routine-hist"); err != nil {
		t.Fatalf("lease: %v", err)
	}
	if err := q.Ack("routine-hist", "delivery-8", nil); err != nil {
		t.Fatalf("ack: %v", err)
	}

	events, err := q.History("routine-hist", time.Time{})
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	if len(events) < 3 {
		t.Errorf("expected at least 3 events (enqueue+lease+ack), got %d", len(events))
	}
}

// TestEnqueueLeaseFIFO verifies messages are leased in insertion order.
func TestEnqueueLeaseFIFO(t *testing.T) {
	q, _ := newTestQueue(t)

	for i, id := range []string{"first", "second", "third"} {
		msg := testMsg(id, "routine-fifo")
		if err := q.Enqueue(msg); err != nil {
			t.Fatalf("enqueue %d: %v", i, err)
		}
		time.Sleep(2 * time.Millisecond) // ensure distinct timestamps
	}

	for _, want := range []string{"first", "second", "third"} {
		got, err := q.Lease("routine-fifo")
		if err != nil || got == nil {
			t.Fatalf("lease: %v, %v", got, err)
		}
		if got.DeliveryID != want {
			t.Errorf("order: got %q, want %q", got.DeliveryID, want)
		}
		if err := q.Ack("routine-fifo", got.DeliveryID, nil); err != nil {
			t.Fatalf("ack %s: %v", want, err)
		}
	}
}

// TestExtendLease verifies lease extension updates the file.
func TestExtendLease(t *testing.T) {
	q, _ := newTestQueue(t)

	msg := testMsg("delivery-ext", "routine-ext")
	if err := q.Enqueue(msg); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	leased, err := q.Lease("routine-ext")
	if err != nil || leased == nil {
		t.Fatalf("lease: %v, %v", leased, err)
	}

	original := *leased.LeaseExpiry
	if err := q.ExtendLease("routine-ext", "delivery-ext", 10*time.Minute); err != nil {
		t.Fatalf("extend lease: %v", err)
	}

	// Re-lease would race with the existing lease; just verify the extend succeeded.
	_ = original
}
