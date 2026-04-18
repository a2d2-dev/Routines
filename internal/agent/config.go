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

// Package agent implements the Routines Agent Runtime: a process that leases
// messages from the Gateway, orchestrates Claude Code child processes, manages
// session continuity, and reports results back via ack/nack.
package agent

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

const (
	// ExitCodeResumable is the Claude Code exit code that signals a successful
	// run that should keep its session for the next message (EX_TEMPFAIL).
	ExitCodeResumable = 75

	// DefaultHeartbeatInterval is how often the agent sends heartbeats to the
	// Gateway while a Claude Code child process is running.
	DefaultHeartbeatInterval = 60 * time.Second

	// DefaultLeasePollWait is the default long-poll wait duration sent to the
	// Gateway on each lease request.
	DefaultLeasePollWait = 30 * time.Second

	// DefaultMaxDuration is the fallback maxDurationSeconds when the env var is
	// not set.
	DefaultMaxDuration = 1800 * time.Second
)

// Config holds the runtime configuration for the Agent Runtime. All fields
// are populated from environment variables injected by the Controller.
type Config struct {
	// GatewayURL is the base HTTP URL of the Gateway (e.g. http://gateway:8080).
	GatewayURL string

	// RoutineUID is the Kubernetes UID of the Routine CR this pod serves.
	RoutineUID string

	// WorkDir is the root of the PVC mount inside the pod (e.g. /work).
	WorkDir string

	// ClaudeBin is the path (or name) of the claude executable.
	ClaudeBin string

	// MaxDuration is the hard upper bound on a single Claude Code run.
	MaxDuration time.Duration

	// HeartbeatInterval is how often the agent sends heartbeats while running.
	HeartbeatInterval time.Duration

	// LeasePollWait is the long-poll wait duration forwarded to GET /v1/lease.
	LeasePollWait time.Duration

	// HTTPTimeout is the HTTP client timeout for Gateway API calls (excluding
	// the long-poll lease call, which uses a separate context deadline).
	HTTPTimeout time.Duration
}

// ConfigFromEnv builds a Config from environment variables.
// Returns an error if any required variable is missing.
func ConfigFromEnv() (Config, error) {
	gatewayURL := os.Getenv("AGENT_GATEWAY_URL")
	if gatewayURL == "" {
		return Config{}, fmt.Errorf("AGENT_GATEWAY_URL is required")
	}

	routineUID := os.Getenv("AGENT_ROUTINE_UID")
	if routineUID == "" {
		return Config{}, fmt.Errorf("AGENT_ROUTINE_UID is required")
	}

	workDir := envOrDefault("AGENT_WORK_DIR", "/work")
	claudeBin := envOrDefault("AGENT_CLAUDE_BIN", "claude")

	maxDuration := DefaultMaxDuration
	if v := os.Getenv("AGENT_MAX_DURATION_SECONDS"); v != "" {
		secs, err := strconv.Atoi(v)
		if err != nil || secs <= 0 {
			return Config{}, fmt.Errorf("AGENT_MAX_DURATION_SECONDS must be a positive integer, got %q", v)
		}
		maxDuration = time.Duration(secs) * time.Second
	}

	heartbeat := DefaultHeartbeatInterval
	if v := os.Getenv("AGENT_HEARTBEAT_INTERVAL_SECONDS"); v != "" {
		secs, err := strconv.Atoi(v)
		if err != nil || secs <= 0 {
			return Config{}, fmt.Errorf("AGENT_HEARTBEAT_INTERVAL_SECONDS must be a positive integer, got %q", v)
		}
		heartbeat = time.Duration(secs) * time.Second
	}

	leasePoll := DefaultLeasePollWait
	if v := os.Getenv("AGENT_LEASE_POLL_SECONDS"); v != "" {
		secs, err := strconv.Atoi(v)
		if err != nil || secs <= 0 {
			return Config{}, fmt.Errorf("AGENT_LEASE_POLL_SECONDS must be a positive integer, got %q", v)
		}
		leasePoll = time.Duration(secs) * time.Second
	}

	return Config{
		GatewayURL:        gatewayURL,
		RoutineUID:        routineUID,
		WorkDir:           workDir,
		ClaudeBin:         claudeBin,
		MaxDuration:       maxDuration,
		HeartbeatInterval: heartbeat,
		LeasePollWait:     leasePoll,
		HTTPTimeout:       10 * time.Second,
	}, nil
}

func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
