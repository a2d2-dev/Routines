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
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/a2d2-dev/routines/internal/gateway"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// Runner is the main Agent Runtime loop. It leases messages from the Gateway,
// orchestrates Claude Code runs, and reports results back.
type Runner struct {
	cfg     Config
	gateway *GatewayClient
	session *SessionManager
	claude  *claudeRunner
}

// NewRunner creates a Runner from cfg.
func NewRunner(cfg Config) *Runner {
	client := NewGatewayClient(cfg.GatewayURL, cfg.HTTPTimeout)
	return &Runner{
		cfg:     cfg,
		gateway: client,
		session: NewSessionManager(cfg.WorkDir),
		claude:  newClaudeRunner(cfg.ClaudeBin, filepath.Join(cfg.WorkDir, "repo"), cfg.MaxDuration),
	}
}

// Run is the outer message-processing loop. It blocks until ctx is cancelled.
func (r *Runner) Run(ctx context.Context) error {
	logger := log.FromContext(ctx).WithName("agent-runner")
	logger.Info("Starting agent runtime loop",
		"gatewayURL", r.cfg.GatewayURL,
		"routineUID", r.cfg.RoutineUID,
	)

	// Ensure core work directories exist.
	if err := r.ensureWorkDirs(); err != nil {
		return fmt.Errorf("ensure work dirs: %w", err)
	}

	for {
		select {
		case <-ctx.Done():
			logger.Info("Agent runtime shutting down")
			return nil
		default:
		}

		msg, err := r.leaseNext(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			logger.Error(err, "Failed to lease message — retrying after backoff")
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(5 * time.Second):
			}
			continue
		}
		if msg == nil {
			// Long-poll expired cleanly — loop immediately.
			continue
		}

		logger.Info("Leased message",
			"deliveryID", msg.DeliveryID,
			"source", msg.Source,
			"retryCount", msg.RetryCount,
		)

		r.processMessage(ctx, msg)
	}
}

// leaseNext performs a long-poll lease request with a context deadline set to
// slightly beyond the wait duration so that the HTTP client does not time out
// before the server responds.
func (r *Runner) leaseNext(ctx context.Context) (*gateway.Message, error) {
	// Add 15 s buffer so the HTTP client deadline is comfortably beyond the
	// server-side wait.
	deadline := time.Now().Add(r.cfg.LeasePollWait + 15*time.Second)
	leaseCtx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()

	// Use a client with an extended timeout for long-poll lease calls.
	client := NewGatewayClient(r.cfg.GatewayURL, r.cfg.LeasePollWait+30*time.Second)
	return client.Lease(leaseCtx, r.cfg.RoutineUID, r.cfg.LeasePollWait)
}

// processMessage handles one leased message end-to-end: reads the prompt,
// launches Claude Code, sends heartbeats, then acks or nacks.
func (r *Runner) processMessage(ctx context.Context, msg *gateway.Message) {
	logger := log.FromContext(ctx).WithName("agent-runner").WithValues(
		"deliveryID", msg.DeliveryID,
		"source", msg.Source,
	)

	// Read the prompt from /work/routine/prompt.md.
	prompt, err := r.readPrompt()
	if err != nil {
		logger.Error(err, "Could not read prompt — nacking message")
		r.nackWithLog(ctx, msg, fmt.Sprintf("read prompt: %v", err), false, logger)
		return
	}

	// Read (or generate) the current session ID.
	sessionID, err := r.session.Read()
	if err != nil {
		logger.Error(err, "Could not read session ID — nacking message")
		r.nackWithLog(ctx, msg, fmt.Sprintf("read session-id: %v", err), false, logger)
		return
	}

	logger.Info("Starting Claude Code run", "sessionID", sessionID)

	logsDir := filepath.Join(r.cfg.WorkDir, "logs", msg.DeliveryID)

	// Start heartbeat goroutine.
	hbCtx, hbCancel := context.WithCancel(ctx)
	defer hbCancel()
	go r.heartbeatLoop(hbCtx, msg.DeliveryID, logger)

	// Extract payload string from the message.
	payload := extractPayload(msg)

	// Run Claude Code.
	result, err := r.claude.Run(ctx, prompt, payload, sessionID, logsDir)
	hbCancel() // stop heartbeats
	if err != nil {
		logger.Error(err, "Claude Code run failed to start")
		r.nackWithLog(ctx, msg, fmt.Sprintf("run claude: %v", err), true, logger)
		return
	}

	logger.Info("Claude Code run finished",
		"exitCode", result.ExitCode,
		"durationMs", result.DurationMs,
		"tokensUsed", result.TokensUsed,
	)

	// Persist the session ID for the next run.
	if result.SessionID != "" {
		if err := r.session.Write(result.SessionID); err != nil {
			logger.Error(err, "Could not persist session ID")
		}
	}

	switch {
	case result.ExitCode == 0 || result.ExitCode == ExitCodeResumable:
		// Success (0) or resumable success (75 EX_TEMPFAIL).
		if err := r.gateway.Ack(ctx, r.cfg.RoutineUID, msg.DeliveryID,
			result.ExitCode, result.DurationMs, result.TokensUsed); err != nil {
			logger.Error(err, "Failed to ack message")
		} else {
			logger.Info("Acked message")
		}
		// Clear session on non-resumable exit so the next run starts fresh.
		if result.ExitCode != ExitCodeResumable {
			_ = r.session.Clear()
		}

	default:
		// Non-zero, non-resumable exit code → failure.
		reason := fmt.Sprintf("claude exited with code %d", result.ExitCode)
		r.nackWithLog(ctx, msg, reason, false, logger)
		_ = r.session.Clear()
	}
}

// heartbeatLoop sends periodic heartbeats to the Gateway until ctx is cancelled.
func (r *Runner) heartbeatLoop(ctx context.Context, deliveryID string, logger logSink) {
	ticker := time.NewTicker(r.cfg.HeartbeatInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := r.gateway.Heartbeat(ctx, r.cfg.RoutineUID, deliveryID, 0); err != nil {
				logger.Error(err, "Failed to send heartbeat")
			}
		}
	}
}

// logSink is a minimal interface subset of logr.Logger used in helpers.
type logSink interface {
	Error(err error, msg string, keysAndValues ...interface{})
}

// readPrompt reads the Claude prompt from /work/routine/prompt.md.
func (r *Runner) readPrompt() (string, error) {
	path := filepath.Join(r.cfg.WorkDir, "routine", "prompt.md")
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", path, err)
	}
	return strings.TrimSpace(string(data)), nil
}

// ensureWorkDirs creates the standard work directories if they don't exist.
func (r *Runner) ensureWorkDirs() error {
	dirs := []string{
		filepath.Join(r.cfg.WorkDir, "routine"),
		filepath.Join(r.cfg.WorkDir, "repo"),
		filepath.Join(r.cfg.WorkDir, "logs"),
		filepath.Join(r.cfg.WorkDir, "sessions"),
	}
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return fmt.Errorf("mkdir %s: %w", d, err)
		}
	}
	return nil
}

// nackWithLog calls Nack and logs any error.
func (r *Runner) nackWithLog(ctx context.Context, msg *gateway.Message, reason string, retryable bool, logger logSink) {
	if err := r.gateway.Nack(ctx, r.cfg.RoutineUID, msg.DeliveryID, reason, retryable); err != nil {
		logger.Error(err, "Failed to nack message")
	}
}

// extractPayload converts msg.Payload (json.RawMessage) to a string suitable
// for embedding in the Claude prompt.
func extractPayload(msg *gateway.Message) string {
	if len(msg.Payload) == 0 {
		return ""
	}
	// If it's a plain JSON string, unwrap it.
	var s string
	if err := json.Unmarshal(msg.Payload, &s); err == nil {
		return s
	}
	// Otherwise return the raw JSON (webhook body, GitHub event, etc.).
	return string(msg.Payload)
}
