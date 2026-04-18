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

// Command gateway is the Routines Gateway binary. It accepts messages from
// schedule, webhook, and GitHub triggers, stores them in a POSIX file queue
// on a shared PVC, and serves a lease/ack/nack HTTP API for Agent Runtime pods.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/a2d2-dev/routines/internal/gateway"
)

func main() {
	var (
		dataRoot            string
		listenAddr          string
		leaseTTL            time.Duration
		reaperInterval      time.Duration
		defaultWait         time.Duration
		maxWait             time.Duration
		githubWebhookSecret string
	)

	flag.StringVar(&dataRoot, "data-root", envOrDefault("GATEWAY_DATA_ROOT", "/data"),
		"Path to the PVC mount point where queues and events are stored.")
	flag.StringVar(&listenAddr, "listen-addr", envOrDefault("GATEWAY_LISTEN_ADDR", ":8080"),
		"TCP address to listen on.")
	flag.DurationVar(&leaseTTL, "lease-ttl", gateway.DefaultLeaseTTL,
		"How long a leased message stays in processing/ before the reaper returns it to inbox/.")
	flag.DurationVar(&reaperInterval, "reaper-interval", gateway.DefaultReaperInterval,
		"How often the reaper goroutine scans for expired leases.")
	flag.DurationVar(&defaultWait, "default-wait", 30*time.Second,
		"Default long-poll wait duration for /v1/lease.")
	flag.DurationVar(&maxWait, "max-wait", 2*time.Minute,
		"Maximum long-poll wait duration for /v1/lease.")
	flag.StringVar(&githubWebhookSecret, "github-webhook-secret",
		os.Getenv("GATEWAY_GITHUB_WEBHOOK_SECRET"),
		"Shared secret for GitHub App webhook validation (HMAC-SHA256).")

	flag.Parse()

	cfg := gateway.Config{
		DataRoot:            dataRoot,
		ListenAddr:          listenAddr,
		LeaseTTL:            leaseTTL,
		ReaperInterval:      reaperInterval,
		DefaultWait:         defaultWait,
		MaxWait:             maxWait,
		GitHubWebhookSecret: githubWebhookSecret,
		WebhookSecrets:      make(map[string]string),
	}

	srv := gateway.NewServer(cfg)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
	defer stop()

	fmt.Fprintf(os.Stderr, "gateway: starting on %s, data-root=%s\n", listenAddr, dataRoot)
	if err := srv.Start(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "gateway: %v\n", err)
		os.Exit(1)
	}
}

func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
