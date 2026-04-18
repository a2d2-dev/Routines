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
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	// DefaultLeaseTTL is how long a leased message stays in processing/ before
	// the reaper moves it back to inbox/.
	DefaultLeaseTTL = 5 * time.Minute

	// DefaultReaperInterval is how often the reaper scans for expired leases.
	DefaultReaperInterval = 30 * time.Second
)

// dirInbox, dirProcessing, dirDone, dirFailed are the sub-directory names
// inside each routine queue directory.
const (
	dirInbox      = "inbox"
	dirProcessing = "processing"
	dirDone       = "done"
	dirFailed     = "failed"
)

// FileQueue is a POSIX-rename-based persistent message queue stored under a
// root directory (typically a PVC mount point).
//
// Layout:
//
//	<root>/queues/<routineUID>/{inbox,processing,done,failed}/<ts>-<deliveryID>.json
//	<root>/events/<routineUID>.jsonl
type FileQueue struct {
	root     string
	leaseTTL time.Duration

	// mu protects notifyChans — no queue file operations need this lock since
	// rename() provides the atomicity guarantee.
	mu          sync.Mutex
	notifyChans map[string][]chan struct{}
}

// NewFileQueue creates a FileQueue rooted at root.
func NewFileQueue(root string, leaseTTL time.Duration) *FileQueue {
	if leaseTTL <= 0 {
		leaseTTL = DefaultLeaseTTL
	}
	return &FileQueue{
		root:        root,
		leaseTTL:    leaseTTL,
		notifyChans: make(map[string][]chan struct{}),
	}
}

// queueDir returns the per-routine queue root.
func (q *FileQueue) queueDir(routineUID string) string {
	return filepath.Join(q.root, "queues", routineUID)
}

// subDir returns a specific sub-directory for routineUID.
func (q *FileQueue) subDir(routineUID, sub string) string {
	return filepath.Join(q.queueDir(routineUID), sub)
}

// eventsFile returns the append-only audit JSONL path for routineUID.
func (q *FileQueue) eventsFile(routineUID string) string {
	return filepath.Join(q.root, "events", routineUID+".jsonl")
}

// msgFilename produces the filename for a message: <nanoseconds>-<deliveryID>.json.
func msgFilename(deliveryID string, t time.Time) string {
	return fmt.Sprintf("%020d-%s.json", t.UnixNano(), deliveryID)
}

// EnsureQueueDirs creates all sub-directories for routineUID if they do not
// exist. Safe to call on every enqueue.
func (q *FileQueue) EnsureQueueDirs(routineUID string) error {
	for _, sub := range []string{dirInbox, dirProcessing, dirDone, dirFailed} {
		if err := os.MkdirAll(q.subDir(routineUID, sub), 0o755); err != nil {
			return fmt.Errorf("mkdir %s/%s: %w", routineUID, sub, err)
		}
	}
	if err := os.MkdirAll(filepath.Join(q.root, "events"), 0o755); err != nil {
		return fmt.Errorf("mkdir events: %w", err)
	}
	return nil
}

// Enqueue writes msg to inbox/ and appends an enqueued event.
func (q *FileQueue) Enqueue(msg *Message) error {
	if err := q.EnsureQueueDirs(msg.RoutineUID); err != nil {
		return err
	}

	msg.EnqueuedAt = time.Now().UTC()
	data, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("marshal message: %w", err)
	}

	filename := msgFilename(msg.DeliveryID, msg.EnqueuedAt)
	inboxPath := filepath.Join(q.subDir(msg.RoutineUID, dirInbox), filename)

	// Write to a temp file in the same directory, then rename for atomicity.
	tmp := inboxPath + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("write tmp: %w", err)
	}
	if err := os.Rename(tmp, inboxPath); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("rename to inbox: %w", err)
	}

	if err := q.appendEvent(msg.RoutineUID, EventEnqueued, msg.DeliveryID, nil); err != nil {
		// Non-fatal: queue file is in place.
		fmt.Fprintf(os.Stderr, "gateway: append event: %v\n", err)
	}

	q.notify(msg.RoutineUID)
	return nil
}

// Lease atomically moves the oldest inbox message for routineUID to
// processing/ and sets its lease metadata. Returns the leased Message or
// (nil, nil) if inbox is empty.
func (q *FileQueue) Lease(routineUID string) (*Message, error) {
	inboxDir := q.subDir(routineUID, dirInbox)
	procDir := q.subDir(routineUID, dirProcessing)

	entries, err := os.ReadDir(inboxDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("readdir inbox: %w", err)
	}

	// Sort by filename ascending (timestamp prefix ensures FIFO ordering).
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name() < entries[j].Name()
	})

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}

		src := filepath.Join(inboxDir, entry.Name())
		dst := filepath.Join(procDir, entry.Name())

		// Read before rename so we can set lease metadata.
		data, err := os.ReadFile(src)
		if err != nil {
			// File may have been raced away; try next.
			continue
		}

		var msg Message
		if err := json.Unmarshal(data, &msg); err != nil {
			continue
		}

		now := time.Now().UTC()
		expiry := now.Add(q.leaseTTL)
		msg.LeasedAt = &now
		msg.LeaseExpiry = &expiry

		updated, err := json.Marshal(&msg)
		if err != nil {
			return nil, fmt.Errorf("marshal leased message: %w", err)
		}

		// Write updated message to tmp in processing/, then rename from inbox.
		tmp := dst + ".tmp"
		if err := os.WriteFile(tmp, updated, 0o644); err != nil {
			return nil, fmt.Errorf("write processing tmp: %w", err)
		}

		// Rename inbox → processing (atomic on POSIX; src must exist).
		if err := os.Rename(src, dst); err != nil {
			_ = os.Remove(tmp)
			// Another leaser raced us; try next file.
			continue
		}

		// Overwrite processing file with the updated lease metadata.
		if err := os.Rename(tmp, dst); err != nil {
			// Leased file exists but without updated metadata; not fatal for correctness.
			_ = os.Remove(tmp)
		}

		if err := q.appendEvent(routineUID, EventLeased, msg.DeliveryID, nil); err != nil {
			fmt.Fprintf(os.Stderr, "gateway: append event: %v\n", err)
		}

		return &msg, nil
	}

	return nil, nil
}

// LeasePoll blocks until either a message becomes available in inbox/ for
// routineUID or ctx is cancelled. It returns at most one message.
func (q *FileQueue) LeasePoll(ctx context.Context, routineUID string, wait time.Duration) (*Message, error) {
	deadline := time.Now().Add(wait)

	ch := make(chan struct{}, 1)
	q.mu.Lock()
	q.notifyChans[routineUID] = append(q.notifyChans[routineUID], ch)
	q.mu.Unlock()

	defer func() {
		q.mu.Lock()
		chans := q.notifyChans[routineUID]
		for i, c := range chans {
			if c == ch {
				q.notifyChans[routineUID] = append(chans[:i], chans[i+1:]...)
				break
			}
		}
		q.mu.Unlock()
	}()

	for {
		msg, err := q.Lease(routineUID)
		if err != nil {
			return nil, err
		}
		if msg != nil {
			return msg, nil
		}

		remaining := time.Until(deadline)
		if remaining <= 0 {
			return nil, nil
		}

		timer := time.NewTimer(remaining)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
			return nil, nil
		case <-ch:
			timer.Stop()
		}
	}
}

// Ack moves a processing message to done/.
func (q *FileQueue) Ack(routineUID, deliveryID string, meta map[string]string) error {
	filename, err := q.findInProcessing(routineUID, deliveryID)
	if err != nil {
		return err
	}
	if filename == "" {
		return fmt.Errorf("message %s not found in processing for routine %s", deliveryID, routineUID)
	}

	src := filepath.Join(q.subDir(routineUID, dirProcessing), filename)
	dst := filepath.Join(q.subDir(routineUID, dirDone), filename)

	if err := q.EnsureQueueDirs(routineUID); err != nil {
		return err
	}
	if err := os.Rename(src, dst); err != nil {
		return fmt.Errorf("rename processing → done: %w", err)
	}

	if err := q.appendEvent(routineUID, EventAcked, deliveryID, meta); err != nil {
		fmt.Fprintf(os.Stderr, "gateway: append event: %v\n", err)
	}
	return nil
}

// Nack moves a processing message back to inbox/ (retryable) or to failed/.
func (q *FileQueue) Nack(routineUID, deliveryID string, retryable bool, meta map[string]string) error {
	filename, err := q.findInProcessing(routineUID, deliveryID)
	if err != nil {
		return err
	}
	if filename == "" {
		return fmt.Errorf("message %s not found in processing for routine %s", deliveryID, routineUID)
	}

	procDir := q.subDir(routineUID, dirProcessing)
	src := filepath.Join(procDir, filename)

	if err := q.EnsureQueueDirs(routineUID); err != nil {
		return err
	}

	if retryable {
		// Read, increment retry count, clear lease metadata, re-enqueue with new timestamp.
		data, err := os.ReadFile(src)
		if err != nil {
			return fmt.Errorf("read processing file: %w", err)
		}
		var msg Message
		if err := json.Unmarshal(data, &msg); err != nil {
			return fmt.Errorf("unmarshal processing message: %w", err)
		}
		msg.RetryCount++
		msg.LeasedAt = nil
		msg.LeaseExpiry = nil
		msg.EnqueuedAt = time.Now().UTC()

		newFilename := msgFilename(msg.DeliveryID, msg.EnqueuedAt)
		dst := filepath.Join(q.subDir(routineUID, dirInbox), newFilename)
		tmp := dst + ".tmp"

		updated, _ := json.Marshal(&msg)
		if err := os.WriteFile(tmp, updated, 0o644); err != nil {
			return fmt.Errorf("write nack tmp: %w", err)
		}
		if err := os.Rename(src, dst); err != nil {
			_ = os.Remove(tmp)
			return fmt.Errorf("rename processing → inbox: %w", err)
		}
		// Replace with updated content.
		_ = os.Rename(tmp, dst)
		q.notify(routineUID)
	} else {
		dst := filepath.Join(q.subDir(routineUID, dirFailed), filename)
		if err := os.Rename(src, dst); err != nil {
			return fmt.Errorf("rename processing → failed: %w", err)
		}
	}

	if err := q.appendEvent(routineUID, EventNacked, deliveryID, meta); err != nil {
		fmt.Fprintf(os.Stderr, "gateway: append event: %v\n", err)
	}
	return nil
}

// ExtendLease updates the LeaseExpiry of a processing message.
func (q *FileQueue) ExtendLease(routineUID, deliveryID string, by time.Duration) error {
	filename, err := q.findInProcessing(routineUID, deliveryID)
	if err != nil {
		return err
	}
	if filename == "" {
		return fmt.Errorf("message %s not found in processing for routine %s", deliveryID, routineUID)
	}

	path := filepath.Join(q.subDir(routineUID, dirProcessing), filename)
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var msg Message
	if err := json.Unmarshal(data, &msg); err != nil {
		return err
	}
	expiry := time.Now().UTC().Add(by)
	msg.LeaseExpiry = &expiry

	updated, _ := json.Marshal(&msg)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, updated, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// History returns all event entries from events/<routineUID>.jsonl, optionally
// filtered to events after sinceTime.
func (q *FileQueue) History(routineUID string, since time.Time) ([]Event, error) {
	path := q.eventsFile(routineUID)
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read events: %w", err)
	}

	var events []Event
	for line := range strings.SplitSeq(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var ev Event
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			continue
		}
		if !since.IsZero() && !ev.Timestamp.After(since) {
			continue
		}
		events = append(events, ev)
	}
	return events, nil
}

// RunReaper continuously scans processing/ directories for expired leases and
// returns them to inbox/. It exits when ctx is cancelled.
func (q *FileQueue) RunReaper(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = DefaultReaperInterval
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			q.reap()
		}
	}
}

func (q *FileQueue) reap() {
	queuesDir := filepath.Join(q.root, "queues")
	routines, err := os.ReadDir(queuesDir)
	if err != nil {
		return
	}
	for _, r := range routines {
		if !r.IsDir() {
			continue
		}
		q.reapRoutine(r.Name())
	}
}

func (q *FileQueue) reapRoutine(routineUID string) {
	procDir := q.subDir(routineUID, dirProcessing)
	entries, err := os.ReadDir(procDir)
	if err != nil {
		return
	}
	now := time.Now().UTC()
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		path := filepath.Join(procDir, entry.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var msg Message
		if err := json.Unmarshal(data, &msg); err != nil {
			continue
		}
		if msg.LeaseExpiry == nil || now.Before(*msg.LeaseExpiry) {
			continue
		}

		// Expired — move back to inbox.
		msg.LeasedAt = nil
		msg.LeaseExpiry = nil
		msg.EnqueuedAt = now
		msg.RetryCount++

		newFilename := msgFilename(msg.DeliveryID, now)
		dst := filepath.Join(q.subDir(routineUID, dirInbox), newFilename)
		tmp := dst + ".tmp"
		updated, _ := json.Marshal(&msg)
		if writeErr := os.WriteFile(tmp, updated, 0o644); writeErr != nil {
			continue
		}
		if renameErr := os.Rename(path, dst); renameErr != nil {
			_ = os.Remove(tmp)
			continue
		}
		_ = os.Rename(tmp, dst)

		_ = q.appendEvent(routineUID, EventExpired, msg.DeliveryID, nil)
		q.notify(routineUID)
	}
}

// findInProcessing returns the filename (not full path) of the message with
// deliveryID in processing/, or "" if not found.
func (q *FileQueue) findInProcessing(routineUID, deliveryID string) (string, error) {
	procDir := q.subDir(routineUID, dirProcessing)
	entries, err := os.ReadDir(procDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", nil
		}
		return "", fmt.Errorf("readdir processing: %w", err)
	}
	for _, entry := range entries {
		if strings.Contains(entry.Name(), deliveryID) {
			return entry.Name(), nil
		}
	}
	return "", nil
}

// appendEvent appends a JSON event line to events/<routineUID>.jsonl.
func (q *FileQueue) appendEvent(routineUID string, kind EventKind, deliveryID string, meta map[string]string) error {
	ev := Event{
		Kind:       kind,
		DeliveryID: deliveryID,
		RoutineUID: routineUID,
		Timestamp:  time.Now().UTC(),
		Metadata:   meta,
	}
	data, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	data = append(data, '\n')

	path := q.eventsFile(routineUID)
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer func() {
		if cerr := f.Close(); cerr != nil && err == nil {
			err = cerr
		}
	}()
	_, err = f.Write(data)
	return err
}

// notify wakes any goroutines waiting for new messages for routineUID.
func (q *FileQueue) notify(routineUID string) {
	q.mu.Lock()
	chans := q.notifyChans[routineUID]
	q.mu.Unlock()
	for _, ch := range chans {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}
