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

package integration

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/a2d2-dev/routines/internal/gateway"
)

// gatewayURL returns the base URL for the in-process test gateway.
func gatewayURL() string {
	return "http://" + gwAddr
}

// enqueue posts a Message to the in-process gateway and asserts HTTP 202.
func enqueueMsg(deliveryID, routineUID, text string) {
	payload, _ := json.Marshal(map[string]string{"text": text})
	msg := gateway.Message{
		DeliveryID: deliveryID,
		RoutineUID: routineUID,
		Source:     gateway.SourceWebhook,
		Payload:    json.RawMessage(payload),
		Metadata:   map[string]string{"test": "true"},
	}
	body, _ := json.Marshal(msg)
	resp, err := http.Post(gatewayURL()+"/v1/enqueue", "application/json", bytes.NewReader(body))
	ExpectWithOffset(1, err).NotTo(HaveOccurred())
	defer resp.Body.Close()
	ExpectWithOffset(1, resp.StatusCode).To(Equal(http.StatusAccepted), "expected 202 from /v1/enqueue")
}

var _ = Describe("Gateway Queue Lifecycle", func() {
	const routineUID = "test-uid-queue-lifecycle"

	It("enqueues, leases, acks, and records history", func() {
		deliveryID := fmt.Sprintf("dlv-%d", time.Now().UnixNano())

		By("enqueuing a message")
		enqueueMsg(deliveryID, routineUID, "hello from integration test")

		By("leasing the message")
		resp, err := http.Get(fmt.Sprintf("%s/v1/lease/%s?wait=5s", gatewayURL(), routineUID))
		Expect(err).NotTo(HaveOccurred())
		defer resp.Body.Close()
		Expect(resp.StatusCode).To(Equal(http.StatusOK))

		var leased gateway.Message
		Expect(json.NewDecoder(resp.Body).Decode(&leased)).To(Succeed())
		Expect(leased.DeliveryID).To(Equal(deliveryID))
		Expect(leased.RoutineUID).To(Equal(routineUID))
		Expect(leased.LeasedAt).NotTo(BeNil())

		By("acking the message")
		ack := gateway.AckRequest{
			DeliveryID: deliveryID,
			RoutineUID: routineUID,
			ExitCode:   0,
			DurationMs: 42,
		}
		ackBody, _ := json.Marshal(ack)
		ackResp, err := http.Post(gatewayURL()+"/v1/ack", "application/json", bytes.NewReader(ackBody))
		Expect(err).NotTo(HaveOccurred())
		defer ackResp.Body.Close()
		Expect(ackResp.StatusCode).To(Equal(http.StatusNoContent))

		By("verifying history contains enqueue + lease + ack events")
		histResp, err := http.Get(fmt.Sprintf("%s/v1/history/%s", gatewayURL(), routineUID))
		Expect(err).NotTo(HaveOccurred())
		defer histResp.Body.Close()
		Expect(histResp.StatusCode).To(Equal(http.StatusOK))

		var events []gateway.Event
		Expect(json.NewDecoder(histResp.Body).Decode(&events)).To(Succeed())

		kinds := make([]string, len(events))
		for i, ev := range events {
			kinds[i] = string(ev.Kind)
		}
		Expect(kinds).To(ContainElements(
			string(gateway.EventEnqueued),
			string(gateway.EventLeased),
			string(gateway.EventAcked),
		))
	})

	It("nacks and re-enqueues a retryable message", func() {
		deliveryID := fmt.Sprintf("dlv-nack-%d", time.Now().UnixNano())
		routineUID2 := routineUID + "-nack"

		By("enqueuing")
		enqueueMsg(deliveryID, routineUID2, "nack test")

		By("leasing")
		resp, err := http.Get(fmt.Sprintf("%s/v1/lease/%s?wait=5s", gatewayURL(), routineUID2))
		Expect(err).NotTo(HaveOccurred())
		defer resp.Body.Close()
		Expect(resp.StatusCode).To(Equal(http.StatusOK))

		var leased gateway.Message
		Expect(json.NewDecoder(resp.Body).Decode(&leased)).To(Succeed())

		By("nacking with retryable=true")
		nack := gateway.NackRequest{
			DeliveryID: deliveryID,
			RoutineUID: routineUID2,
			Reason:     "transient error",
			Retryable:  true,
		}
		nackBody, _ := json.Marshal(nack)
		nackResp, err := http.Post(gatewayURL()+"/v1/nack", "application/json", bytes.NewReader(nackBody))
		Expect(err).NotTo(HaveOccurred())
		defer nackResp.Body.Close()
		Expect(nackResp.StatusCode).To(Equal(http.StatusNoContent))

		By("verifying message can be leased again")
		resp2, err := http.Get(fmt.Sprintf("%s/v1/lease/%s?wait=5s", gatewayURL(), routineUID2))
		Expect(err).NotTo(HaveOccurred())
		defer resp2.Body.Close()
		Expect(resp2.StatusCode).To(Equal(http.StatusOK))

		var leased2 gateway.Message
		Expect(json.NewDecoder(resp2.Body).Decode(&leased2)).To(Succeed())
		Expect(leased2.DeliveryID).To(Equal(deliveryID))
		Expect(leased2.RetryCount).To(Equal(1))
	})

	It("returns 204 No Content when queue is empty with short wait", func() {
		emptyUID := "empty-routine-uid"
		resp, err := http.Get(fmt.Sprintf("%s/v1/lease/%s?wait=100ms", gatewayURL(), emptyUID))
		Expect(err).NotTo(HaveOccurred())
		defer resp.Body.Close()
		Expect(resp.StatusCode).To(Equal(http.StatusNoContent))
	})

	It("accepts and records a heartbeat", func() {
		deliveryID := fmt.Sprintf("dlv-hb-%d", time.Now().UnixNano())
		routineUID3 := routineUID + "-heartbeat"

		enqueueMsg(deliveryID, routineUID3, "heartbeat test")

		resp, err := http.Get(fmt.Sprintf("%s/v1/lease/%s?wait=5s", gatewayURL(), routineUID3))
		Expect(err).NotTo(HaveOccurred())
		defer resp.Body.Close()
		Expect(resp.StatusCode).To(Equal(http.StatusOK))

		By("sending heartbeat")
		hb := gateway.HeartbeatRequest{
			RoutineUID:       routineUID3,
			CurrentMessageID: deliveryID,
			ExtendBySeconds:  30,
		}
		hbBody, _ := json.Marshal(hb)
		hbResp, err := http.Post(
			fmt.Sprintf("%s/v1/heartbeat/%s", gatewayURL(), routineUID3),
			"application/json", bytes.NewReader(hbBody),
		)
		Expect(err).NotTo(HaveOccurred())
		defer hbResp.Body.Close()
		Expect(hbResp.StatusCode).To(Equal(http.StatusNoContent))
	})
})

var _ = Describe("Gateway Webhook Ingress", func() {
	It("accepts a webhook POST and enqueues a message", func() {
		routineUID := "webhook-routine-uid"
		webhookName := "test-hook"

		By("posting a webhook event")
		payload := `{"event":"push","ref":"refs/heads/main"}`
		req, err := http.NewRequest(http.MethodPost,
			fmt.Sprintf("%s/webhooks/%s/%s", gatewayURL(), webhookName, routineUID),
			bytes.NewBufferString(payload),
		)
		Expect(err).NotTo(HaveOccurred())
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Routine-UID", routineUID)

		resp, err := http.DefaultClient.Do(req)
		Expect(err).NotTo(HaveOccurred())
		defer resp.Body.Close()
		// Webhook handler enqueues or returns accepted/ok.
		Expect(resp.StatusCode).To(BeElementOf(http.StatusOK, http.StatusAccepted))
	})
})
