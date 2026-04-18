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
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/a2d2-dev/routines/internal/gateway"
)

// GatewayClient is an HTTP client for the Routines Gateway API.
type GatewayClient struct {
	baseURL    string
	httpClient *http.Client
}

// NewGatewayClient creates a GatewayClient pointing at baseURL.
func NewGatewayClient(baseURL string, timeout time.Duration) *GatewayClient {
	return &GatewayClient{
		baseURL:    baseURL,
		httpClient: &http.Client{Timeout: timeout},
	}
}

// Lease performs a long-poll GET /v1/lease/{routineUID}?wait={wait} and
// returns the next available message, or nil if the poll expired with no
// message.
func (c *GatewayClient) Lease(ctx context.Context, routineUID string, wait time.Duration) (*gateway.Message, error) {
	url := fmt.Sprintf("%s/v1/lease/%s?wait=%s", c.baseURL, routineUID, wait.String())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("build lease request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("lease request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNoContent {
		// No message available; long-poll expired cleanly.
		return nil, nil
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("lease: unexpected status %d: %s", resp.StatusCode, string(body))
	}

	var msg gateway.Message
	if err := json.NewDecoder(resp.Body).Decode(&msg); err != nil {
		return nil, fmt.Errorf("decode lease response: %w", err)
	}
	return &msg, nil
}

// Ack reports successful completion of a message.
func (c *GatewayClient) Ack(ctx context.Context, routineUID, deliveryID string, exitCode int, durationMs, tokensUsed int64) error {
	body := gateway.AckRequest{
		DeliveryID: deliveryID,
		RoutineUID: routineUID,
		ExitCode:   exitCode,
		DurationMs: durationMs,
		TokensUsed: tokensUsed,
	}
	return c.post(ctx, "/v1/ack", body)
}

// Nack reports a failed message.
func (c *GatewayClient) Nack(ctx context.Context, routineUID, deliveryID, reason string, retryable bool) error {
	body := gateway.NackRequest{
		DeliveryID: deliveryID,
		RoutineUID: routineUID,
		Reason:     reason,
		Retryable:  retryable,
	}
	return c.post(ctx, "/v1/nack", body)
}

// Heartbeat sends a heartbeat to extend the lease on a message in progress.
func (c *GatewayClient) Heartbeat(ctx context.Context, routineUID, currentMessageID string, extendBySeconds int) error {
	body := gateway.HeartbeatRequest{
		RoutineUID:       routineUID,
		CurrentMessageID: currentMessageID,
		ExtendBySeconds:  extendBySeconds,
	}
	return c.post(ctx, fmt.Sprintf("/v1/heartbeat/%s", routineUID), body)
}

// post is a helper that JSON-encodes body, POSTs to path, and returns any
// non-2xx error.
func (c *GatewayClient) post(ctx context.Context, path string, body interface{}) error {
	data, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}

	url := c.baseURL + path
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("POST %s: %w", path, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("POST %s: status %d: %s", path, resp.StatusCode, string(b))
	}
	return nil
}
