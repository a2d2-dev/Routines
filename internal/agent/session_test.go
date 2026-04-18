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
	"os"
	"testing"
)

func TestSessionManager_ReadWriteClear(t *testing.T) {
	dir := t.TempDir()
	sm := NewSessionManager(dir)

	// First Read generates a UUID.
	id1, err := sm.Read()
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if id1 == "" {
		t.Fatal("Read() returned empty session ID")
	}

	// Second Read returns the same ID (persisted by the first call).
	id2, err := sm.Read()
	if err != nil {
		t.Fatalf("second Read() error = %v", err)
	}
	if id1 != id2 {
		t.Errorf("second Read() = %q, want %q", id2, id1)
	}

	// Write a known ID.
	if err := sm.Write("my-session-123"); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	id3, err := sm.Read()
	if err != nil {
		t.Fatalf("Read after Write() error = %v", err)
	}
	if id3 != "my-session-123" {
		t.Errorf("Read after Write() = %q, want my-session-123", id3)
	}

	// Clear removes the file; next Read generates a new UUID.
	if err := sm.Clear(); err != nil {
		t.Fatalf("Clear() error = %v", err)
	}
	if _, err := os.Stat(dir + "/session-id"); !os.IsNotExist(err) {
		t.Error("session-id file should not exist after Clear()")
	}
	id4, err := sm.Read()
	if err != nil {
		t.Fatalf("Read after Clear() error = %v", err)
	}
	if id4 == id3 {
		t.Error("Read after Clear() should return a new UUID, not the cleared value")
	}
}
