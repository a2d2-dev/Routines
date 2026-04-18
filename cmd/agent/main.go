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

// Command agent is the Routines Agent Runtime binary.
//
// It runs as the main container inside each Routine Pod. On startup it:
//  1. Reads configuration from environment variables injected by the Controller.
//  2. Enters a long-poll lease loop against the Gateway.
//  3. For each leased message: reads the prompt, launches Claude Code as a
//     child process, sends periodic heartbeats, and reports the result via
//     ack or nack.
//
// Environment variables:
//
//	AGENT_GATEWAY_URL              (required) Base URL of the Gateway, e.g. http://gateway:8080
//	AGENT_ROUTINE_UID              (required) UID of the Routine CR this pod serves
//	AGENT_WORK_DIR                 (optional, default /work) Root of the PVC mount
//	AGENT_CLAUDE_BIN               (optional, default claude) Path/name of the claude binary
//	AGENT_MAX_DURATION_SECONDS     (optional, default 1800) Hard limit per run
//	AGENT_HEARTBEAT_INTERVAL_SECONDS (optional, default 60) How often to send heartbeats
//	AGENT_LEASE_POLL_SECONDS       (optional, default 30) Long-poll wait duration
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	"github.com/a2d2-dev/routines/internal/agent"
)

func main() {
	// Set up structured logging.
	opts := zap.Options{Development: false}
	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))
	logger := ctrl.Log.WithName("agent")

	cfg, err := agent.ConfigFromEnv()
	if err != nil {
		fmt.Fprintf(os.Stderr, "agent: configuration error: %v\n", err)
		os.Exit(1)
	}

	logger.Info("Agent Runtime starting",
		"gatewayURL", cfg.GatewayURL,
		"routineUID", cfg.RoutineUID,
		"workDir", cfg.WorkDir,
		"claudeBin", cfg.ClaudeBin,
		"maxDuration", cfg.MaxDuration,
	)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
	defer stop()

	runner := agent.NewRunner(cfg)
	if err := runner.Run(ctx); err != nil {
		logger.Error(err, "Agent Runtime exited with error")
		os.Exit(1)
	}
}
