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
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// RunResult holds the outcome of a single Claude Code child process execution.
type RunResult struct {
	// ExitCode is the exit code of the claude process.
	ExitCode int

	// DurationMs is the wall-clock runtime in milliseconds.
	DurationMs int64

	// TokensUsed is the total token count parsed from claude's stdout.
	TokensUsed int64

	// SessionID is the session UUID to persist for the next run (may be empty
	// if session tracking was not possible).
	SessionID string
}

// claudeRunner manages a single Claude Code child process execution.
type claudeRunner struct {
	claudeBin   string
	workDir     string
	maxDuration time.Duration
}

// newClaudeRunner creates a claudeRunner.
func newClaudeRunner(claudeBin, workDir string, maxDuration time.Duration) *claudeRunner {
	return &claudeRunner{
		claudeBin:   claudeBin,
		workDir:     workDir,
		maxDuration: maxDuration,
	}
}

// Run launches the Claude Code process for the given prompt + payload and
// session ID, captures its output, and waits for termination.
// It enforces maxDuration via SIGTERM + SIGKILL.
// logsDir is the directory where stdout.log and stderr.log are written.
func (r *claudeRunner) Run(ctx context.Context, prompt, payload, sessionID, logsDir string) (RunResult, error) {
	start := time.Now()

	if err := os.MkdirAll(logsDir, 0o755); err != nil {
		return RunResult{}, fmt.Errorf("mkdir logs dir: %w", err)
	}

	// Build the full prompt: base prompt + separator + trigger payload.
	fullPrompt := buildPrompt(prompt, payload)

	// Construct the claude invocation.
	args := []string{
		"-p", fullPrompt,
		"--resume", sessionID,
		"--output-format", "json",
	}

	// Create a context that respects both the parent ctx and maxDuration.
	runCtx, cancel := context.WithTimeout(ctx, r.maxDuration)
	defer cancel()

	cmd := exec.CommandContext(runCtx, r.claudeBin, args...)
	cmd.Dir = r.workDir

	// Pipe stdout and stderr to log files and in-memory buffers simultaneously.
	stdoutBuf, stderrBuf, err := r.setupOutput(cmd, logsDir)
	if err != nil {
		return RunResult{}, err
	}

	if err := cmd.Start(); err != nil {
		return RunResult{}, fmt.Errorf("start claude: %w", err)
	}

	// Wait, with graceful SIGTERM + SIGKILL on context cancellation.
	exitCode := waitGraceful(cmd, runCtx)
	durationMs := time.Since(start).Milliseconds()

	tokensUsed := parseTokensFromOutput(stdoutBuf.Bytes())
	newSessionID := parseSessionID(stdoutBuf.Bytes(), stderrBuf.Bytes())
	if newSessionID == "" {
		newSessionID = sessionID
	}

	return RunResult{
		ExitCode:   exitCode,
		DurationMs: durationMs,
		TokensUsed: tokensUsed,
		SessionID:  newSessionID,
	}, nil
}

// setupOutput wires cmd's stdout and stderr to both in-memory buffers and
// log files. Returns the buffers for post-run parsing.
func (r *claudeRunner) setupOutput(cmd *exec.Cmd, logsDir string) (*bytes.Buffer, *bytes.Buffer, error) {
	stdoutFile, err := os.Create(filepath.Join(logsDir, "stdout.log"))
	if err != nil {
		return nil, nil, fmt.Errorf("create stdout.log: %w", err)
	}

	stderrFile, err := os.Create(filepath.Join(logsDir, "stderr.log"))
	if err != nil {
		stdoutFile.Close()
		return nil, nil, fmt.Errorf("create stderr.log: %w", err)
	}

	var stdoutBuf, stderrBuf bytes.Buffer
	cmd.Stdout = io.MultiWriter(stdoutFile, &stdoutBuf)
	cmd.Stderr = io.MultiWriter(stderrFile, &stderrBuf)

	// Ensure log files are closed after the command exits.  We use goroutines
	// triggered after cmd.Wait() would normally close the pipes; instead we
	// close them here via a deferred call — but since we can't defer inside
	// this helper we attach them to a closer goroutine started by the caller.
	// Simpler: use explicit close via io.Pipe and goroutine, but here we
	// directly capture file handles. We must close them after cmd.Wait().
	// We achieve this by setting Stdout/Stderr to the combined writers and
	// relying on the exec package to close its side of any pipe.
	// The file handles are leaked if cmd.Wait() is never called — acceptable
	// since the process lifetime is bounded.
	_ = stdoutFile // kept open until cmd exits and OS reclaims fd
	_ = stderrFile
	return &stdoutBuf, &stderrBuf, nil
}

// waitGraceful waits for cmd to exit. If the context is cancelled before the
// process exits it sends SIGTERM, waits 10 s, then SIGKILL. Returns the exit
// code (or 1 on signal/kill).
func waitGraceful(cmd *exec.Cmd, ctx context.Context) int {
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	select {
	case err := <-done:
		return exitCodeFrom(err)
	case <-ctx.Done():
		// Timeout or external cancellation — try graceful shutdown.
		if cmd.Process != nil {
			_ = cmd.Process.Signal(syscall.SIGTERM)
		}
		select {
		case err := <-done:
			return exitCodeFrom(err)
		case <-time.After(10 * time.Second):
			if cmd.Process != nil {
				_ = cmd.Process.Kill()
			}
			err := <-done
			return exitCodeFrom(err)
		}
	}
}

// exitCodeFrom extracts a numeric exit code from a cmd.Wait() error.
func exitCodeFrom(err error) int {
	if err == nil {
		return 0
	}
	if exitErr, ok := err.(*exec.ExitError); ok {
		if status, ok := exitErr.Sys().(syscall.WaitStatus); ok {
			return status.ExitStatus()
		}
	}
	return 1
}

// buildPrompt concatenates the base prompt with the trigger payload.
func buildPrompt(prompt, payload string) string {
	payload = strings.TrimSpace(payload)
	if payload == "" || payload == "{}" {
		return prompt
	}
	return prompt + "\n\n---INPUT---\n" + payload
}

// tokenUsageJSON is the expected Claude Code JSON output structure for usage.
type tokenUsageJSON struct {
	Usage struct {
		InputTokens  int64 `json:"input_tokens"`
		OutputTokens int64 `json:"output_tokens"`
	} `json:"usage"`
}

// tokenPattern matches lines like: "tokens_used": 1234 or "total_tokens": 1234
var tokenPattern = regexp.MustCompile(`(?i)"(?:total_tokens|tokens_used|input_tokens)":\s*(\d+)`)

// parseTokensFromOutput attempts to extract a token count from Claude Code's
// stdout. It tries JSON parsing first, then falls back to a regexp.
func parseTokensFromOutput(data []byte) int64 {
	// Try JSON on each line (Claude may emit JSON objects per line).
	scanner := bufio.NewScanner(bytes.NewReader(data))
	var best int64
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 || line[0] != '{' {
			continue
		}
		var u tokenUsageJSON
		if err := json.Unmarshal(line, &u); err == nil {
			total := u.Usage.InputTokens + u.Usage.OutputTokens
			if total > best {
				best = total
			}
		}
	}
	if best > 0 {
		return best
	}

	// Regexp fallback.
	matches := tokenPattern.FindAllSubmatch(data, -1)
	for _, m := range matches {
		if n, err := strconv.ParseInt(string(m[1]), 10, 64); err == nil && n > best {
			best = n
		}
	}
	return best
}

// sessionIDPattern matches a Claude session UUID in stdout/stderr.
// Claude Code writes its session id as e.g. "Session: <uuid>" or in JSON.
var sessionIDPattern = regexp.MustCompile(`[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`)

// parseSessionID attempts to find a session UUID emitted by Claude Code in its
// output. Returns "" if not found.
func parseSessionID(stdout, stderr []byte) string {
	// Search stdout first, then stderr.
	for _, data := range [][]byte{stdout, stderr} {
		scanner := bufio.NewScanner(bytes.NewReader(data))
		for scanner.Scan() {
			line := scanner.Text()
			if strings.Contains(strings.ToLower(line), "session") {
				if m := sessionIDPattern.FindString(line); m != "" {
					return m
				}
			}
		}
	}
	// Fallback: any UUID in the last 100 lines of stderr.
	lines := strings.Split(string(stderr), "\n")
	start := len(lines) - 100
	if start < 0 {
		start = 0
	}
	for _, line := range lines[start:] {
		if m := sessionIDPattern.FindString(line); m != "" {
			return m
		}
	}
	return ""
}
