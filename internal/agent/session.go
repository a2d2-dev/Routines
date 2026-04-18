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
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
)

// sessionFile is the path within workDir where the current Claude session ID
// is persisted between message runs.
const sessionFile = "session-id"

// SessionManager handles reading and writing the Claude Code session ID from
// the PVC work directory.
type SessionManager struct {
	workDir string
}

// NewSessionManager creates a SessionManager rooted at workDir.
func NewSessionManager(workDir string) *SessionManager {
	return &SessionManager{workDir: workDir}
}

// Read returns the current session ID, or a new UUID if the file does not
// exist or is empty.
func (s *SessionManager) Read() (string, error) {
	path := filepath.Join(s.workDir, sessionFile)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return s.fresh()
		}
		return "", fmt.Errorf("read session-id: %w", err)
	}
	id := strings.TrimSpace(string(data))
	if id == "" {
		return s.fresh()
	}
	return id, nil
}

// Write persists sessionID to the session file, replacing any previous value.
func (s *SessionManager) Write(sessionID string) error {
	path := filepath.Join(s.workDir, sessionFile)
	if err := os.MkdirAll(s.workDir, 0o755); err != nil {
		return fmt.Errorf("mkdir workDir: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(sessionID+"\n"), 0o644); err != nil {
		return fmt.Errorf("write session-id tmp: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("rename session-id: %w", err)
	}
	return nil
}

// Clear removes the session file so the next run starts a fresh session.
func (s *SessionManager) Clear() error {
	path := filepath.Join(s.workDir, sessionFile)
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("clear session-id: %w", err)
	}
	return nil
}

func (s *SessionManager) fresh() (string, error) {
	id := uuid.New().String()
	if err := s.Write(id); err != nil {
		return "", err
	}
	return id, nil
}
