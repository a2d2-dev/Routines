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

// Command routines is the Routines CLI — a kubectl-style tool for managing
// Routine CRs and interacting with the Gateway API.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/client"

	routinesv1alpha1 "github.com/a2d2-dev/routines/api/v1alpha1"
)

// globalFlags holds flags shared across all commands.
type globalFlags struct {
	kubeconfig string
	namespace  string
	gatewayURL string
}

var globals globalFlags

func main() {
	root := &cobra.Command{
		Use:   "routines",
		Short: "CLI for managing Routines — self-hosted, K8s-native AI agent schedules",
		Long: `routines is a kubectl-style CLI for the Routines operator.

It reads Routine CRs from Kubernetes and interacts with the Routines Gateway
to enqueue messages, query history, and stream agent logs.

Examples:
  routines list
  routines describe my-routine
  routines msg my-routine "Run the nightly audit"
  routines history my-routine
  routines logs my-routine
  routines suspend my-routine
  routines resume my-routine`,
	}

	// Global flags
	root.PersistentFlags().StringVar(&globals.kubeconfig, "kubeconfig",
		os.Getenv("KUBECONFIG"), "Path to the kubeconfig file (default: $KUBECONFIG or ~/.kube/config)")
	root.PersistentFlags().StringVarP(&globals.namespace, "namespace", "n",
		envOrDefault("ROUTINES_NAMESPACE", "default"), "Kubernetes namespace")
	root.PersistentFlags().StringVar(&globals.gatewayURL, "gateway-url",
		os.Getenv("ROUTINES_GATEWAY_URL"), "Gateway base URL (e.g. http://gateway:8080). Auto-detected if empty.")

	root.AddCommand(
		newListCmd(),
		newDescribeCmd(),
		newMsgCmd(),
		newHistoryCmd(),
		newLogsCmd(),
		newSuspendCmd(),
		newResumeCmd(),
	)

	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}

// ---------------------------------------------------------------------------
// list
// ---------------------------------------------------------------------------

func newListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List Routine CRs in the current namespace",
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := newK8sClient()
			if err != nil {
				return err
			}
			var list routinesv1alpha1.RoutineList
			if err := c.List(cmd.Context(), &list,
				client.InNamespace(globals.namespace)); err != nil {
				return fmt.Errorf("list routines: %w", err)
			}

			tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 3, ' ', 0)
			fmt.Fprintln(tw, "NAME\tPHASE\tPOD READY\tSUSPENDED\tAGE")
			for _, r := range list.Items {
				age := time.Since(r.CreationTimestamp.Time).Round(time.Second)
				fmt.Fprintf(tw, "%s\t%s\t%v\t%v\t%s\n",
					r.Name,
					r.Status.Phase,
					r.Status.PodReady,
					r.Spec.Suspend,
					age,
				)
			}
			return tw.Flush()
		},
	}
}

// ---------------------------------------------------------------------------
// describe
// ---------------------------------------------------------------------------

func newDescribeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "describe <routine>",
		Short: "Show Routine details and recent message history",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			c, err := newK8sClient()
			if err != nil {
				return err
			}
			var r routinesv1alpha1.Routine
			if err := c.Get(cmd.Context(), types.NamespacedName{
				Namespace: globals.namespace, Name: name,
			}, &r); err != nil {
				return fmt.Errorf("get routine %q: %w", name, err)
			}

			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "Name:        %s\n", r.Name)
			fmt.Fprintf(out, "Namespace:   %s\n", r.Namespace)
			fmt.Fprintf(out, "UID:         %s\n", r.UID)
			fmt.Fprintf(out, "Phase:       %s\n", r.Status.Phase)
			fmt.Fprintf(out, "Pod Ready:   %v\n", r.Status.PodReady)
			fmt.Fprintf(out, "Suspended:   %v\n", r.Spec.Suspend)
			if r.Status.LastMessageAt != nil {
				fmt.Fprintf(out, "Last Msg At: %s\n", r.Status.LastMessageAt.Format(time.RFC3339))
			}
			if r.Status.CurrentMessageID != "" {
				fmt.Fprintf(out, "Current Msg: %s\n", r.Status.CurrentMessageID)
			}
			if len(r.Spec.TriggerRefs) > 0 {
				fmt.Fprintf(out, "Triggers:\n")
				for _, t := range r.Spec.TriggerRefs {
					fmt.Fprintf(out, "  - %s/%s\n", t.Kind, t.Name)
				}
			}
			if r.Spec.Prompt.Inline != "" {
				preview := r.Spec.Prompt.Inline
				if len(preview) > 120 {
					preview = preview[:120] + "…"
				}
				fmt.Fprintf(out, "Prompt:      %s\n", preview)
			} else if r.Spec.Prompt.ConfigMapRef != nil {
				fmt.Fprintf(out, "Prompt:      configmap %s/%s\n",
					r.Spec.Prompt.ConfigMapRef.Name, r.Spec.Prompt.ConfigMapRef.Key)
			}

			// Print recent history from the Gateway.
			gwURL, err := resolveGatewayURL(cmd.Context(), c)
			if err == nil {
				events, err := fetchHistory(cmd.Context(), gwURL, string(r.UID), time.Now().Add(-24*time.Hour))
				if err == nil && len(events) > 0 {
					fmt.Fprintln(out, "\nRecent Messages (last 24h):")
					tw := tabwriter.NewWriter(out, 0, 0, 3, ' ', 0)
					fmt.Fprintln(tw, "  KIND\tDELIVERY-ID\tTIMESTAMP")
					for _, ev := range events {
						fmt.Fprintf(tw, "  %s\t%s\t%s\n",
							ev.Kind, ev.DeliveryID[:min(8, len(ev.DeliveryID))],
							ev.Timestamp.Format(time.RFC3339))
					}
					_ = tw.Flush()
				}
			}

			return nil
		},
	}
}

// ---------------------------------------------------------------------------
// msg
// ---------------------------------------------------------------------------

func newMsgCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "msg <routine> <text>",
		Short: "Enqueue a message to a Routine via the Gateway",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			name, text := args[0], args[1]
			c, err := newK8sClient()
			if err != nil {
				return err
			}
			var r routinesv1alpha1.Routine
			if err := c.Get(cmd.Context(), types.NamespacedName{
				Namespace: globals.namespace, Name: name,
			}, &r); err != nil {
				return fmt.Errorf("get routine %q: %w", name, err)
			}

			gwURL, err := resolveGatewayURL(cmd.Context(), c)
			if err != nil {
				return fmt.Errorf("resolve gateway URL: %w", err)
			}

			deliveryID := newUUID()
			payload, _ := json.Marshal(map[string]string{"text": text})
			msg := map[string]interface{}{
				"deliveryID": deliveryID,
				"routineUID": string(r.UID),
				"source":     "cli",
				"payload":    json.RawMessage(payload),
				"metadata":   map[string]string{"cli.user": os.Getenv("USER")},
			}
			body, _ := json.Marshal(msg)

			resp, err := http.Post(gwURL+"/v1/enqueue", "application/json", bytes.NewReader(body))
			if err != nil {
				return fmt.Errorf("enqueue: %w", err)
			}
			defer resp.Body.Close()
			respBody, _ := io.ReadAll(resp.Body)

			if resp.StatusCode != http.StatusAccepted {
				return fmt.Errorf("gateway returned %d: %s", resp.StatusCode, respBody)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Enqueued: %s\n", deliveryID)
			return nil
		},
	}
}

// ---------------------------------------------------------------------------
// history
// ---------------------------------------------------------------------------

func newHistoryCmd() *cobra.Command {
	var since string
	cmd := &cobra.Command{
		Use:   "history <routine>",
		Short: "Show Gateway message history for a Routine",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			c, err := newK8sClient()
			if err != nil {
				return err
			}
			var r routinesv1alpha1.Routine
			if err := c.Get(cmd.Context(), types.NamespacedName{
				Namespace: globals.namespace, Name: name,
			}, &r); err != nil {
				return fmt.Errorf("get routine %q: %w", name, err)
			}

			gwURL, err := resolveGatewayURL(cmd.Context(), c)
			if err != nil {
				return fmt.Errorf("resolve gateway URL: %w", err)
			}

			var sinceTime time.Time
			if since != "" {
				sinceTime, err = time.Parse(time.RFC3339, since)
				if err != nil {
					return fmt.Errorf("invalid --since (expected RFC3339): %w", err)
				}
			}

			events, err := fetchHistory(cmd.Context(), gwURL, string(r.UID), sinceTime)
			if err != nil {
				return fmt.Errorf("fetch history: %w", err)
			}
			if len(events) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "No events found.")
				return nil
			}

			tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 3, ' ', 0)
			fmt.Fprintln(tw, "KIND\tDELIVERY-ID\tTIMESTAMP")
			for _, ev := range events {
				fmt.Fprintf(tw, "%s\t%s\t%s\n",
					ev.Kind, ev.DeliveryID, ev.Timestamp.Format(time.RFC3339))
			}
			return tw.Flush()
		},
	}
	cmd.Flags().StringVar(&since, "since", "", "Only show events after this RFC3339 timestamp")
	return cmd
}

// ---------------------------------------------------------------------------
// logs
// ---------------------------------------------------------------------------

func newLogsCmd() *cobra.Command {
	var messageID string
	var follow bool
	var tail int64

	cmd := &cobra.Command{
		Use:   "logs <routine>",
		Short: "Stream or print agent pod logs for a Routine",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			cfg, err := buildRESTConfig()
			if err != nil {
				return err
			}
			c, err := newK8sClient()
			if err != nil {
				return err
			}

			var r routinesv1alpha1.Routine
			if err := c.Get(cmd.Context(), types.NamespacedName{
				Namespace: globals.namespace, Name: name,
			}, &r); err != nil {
				return fmt.Errorf("get routine %q: %w", name, err)
			}

			// Find the agent pod: labeled with routine name.
			clientset, err := kubernetes.NewForConfig(cfg)
			if err != nil {
				return fmt.Errorf("build clientset: %w", err)
			}

			selector := fmt.Sprintf("routines.a2d2.dev/routine=%s", name)
			pods, err := clientset.CoreV1().Pods(globals.namespace).List(cmd.Context(), metav1.ListOptions{
				LabelSelector: selector,
			})
			if err != nil {
				return fmt.Errorf("list pods: %w", err)
			}
			if len(pods.Items) == 0 {
				return fmt.Errorf("no pods found for routine %q (selector: %s)", name, selector)
			}

			pod := pods.Items[0]
			logOpts := &corev1.PodLogOptions{
				Follow: follow,
			}
			if tail > 0 {
				logOpts.TailLines = &tail
			}
			if messageID != "" {
				// Filter by message ID is best-effort via grep in the future;
				// for now we just note it in a header comment.
				fmt.Fprintf(cmd.OutOrStdout(), "# Logs for pod %s (message-id filter: %s)\n", pod.Name, messageID)
			}

			req := clientset.CoreV1().Pods(globals.namespace).GetLogs(pod.Name, logOpts)
			stream, err := req.Stream(cmd.Context())
			if err != nil {
				return fmt.Errorf("get log stream: %w", err)
			}
			defer stream.Close()

			_, err = io.Copy(cmd.OutOrStdout(), stream)
			return err
		},
	}
	cmd.Flags().StringVar(&messageID, "message-id", "", "Filter logs to a specific message delivery ID (informational)")
	cmd.Flags().BoolVarP(&follow, "follow", "f", false, "Stream logs (follow)")
	cmd.Flags().Int64Var(&tail, "tail", 100, "Number of recent log lines to show (0 = all)")
	return cmd
}

// ---------------------------------------------------------------------------
// suspend / resume
// ---------------------------------------------------------------------------

func newSuspendCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "suspend <routine>",
		Short: "Suspend a Routine (scales agent pod to zero, retains PVC)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return patchSuspend(cmd.Context(), args[0], true)
		},
	}
}

func newResumeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "resume <routine>",
		Short: "Resume a suspended Routine",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return patchSuspend(cmd.Context(), args[0], false)
		},
	}
}

func patchSuspend(ctx context.Context, name string, suspend bool) error {
	c, err := newK8sClient()
	if err != nil {
		return err
	}
	var r routinesv1alpha1.Routine
	if err := c.Get(ctx, types.NamespacedName{
		Namespace: globals.namespace, Name: name,
	}, &r); err != nil {
		return fmt.Errorf("get routine %q: %w", name, err)
	}
	patch := client.MergeFrom(r.DeepCopy())
	r.Spec.Suspend = suspend
	if err := c.Patch(ctx, &r, patch); err != nil {
		return fmt.Errorf("patch routine %q: %w", name, err)
	}
	verb := "Suspended"
	if !suspend {
		verb = "Resumed"
	}
	fmt.Printf("%s %s\n", verb, name)
	return nil
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// gatewayEvent mirrors gateway.Event for JSON decoding without importing the
// internal package (to keep the CLI binary standalone).
type gatewayEvent struct {
	Kind       string            `json:"kind"`
	DeliveryID string            `json:"deliveryID"`
	RoutineUID string            `json:"routineUID"`
	Timestamp  time.Time         `json:"timestamp"`
	Metadata   map[string]string `json:"metadata,omitempty"`
}

func fetchHistory(ctx context.Context, gwURL, routineUID string, since time.Time) ([]gatewayEvent, error) {
	url := fmt.Sprintf("%s/v1/history/%s", gwURL, routineUID)
	if !since.IsZero() {
		url += "?since=" + since.UTC().Format(time.RFC3339)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("gateway %d: %s", resp.StatusCode, body)
	}

	var events []gatewayEvent
	if err := json.NewDecoder(resp.Body).Decode(&events); err != nil {
		return nil, fmt.Errorf("decode history: %w", err)
	}
	return events, nil
}

// resolveGatewayURL returns the gateway base URL. Priority:
//  1. --gateway-url flag / ROUTINES_GATEWAY_URL env
//  2. K8s service routines-gateway in the routines-system namespace
//  3. K8s service routines-gateway in the target namespace
func resolveGatewayURL(ctx context.Context, c client.Client) (string, error) {
	if globals.gatewayURL != "" {
		return strings.TrimRight(globals.gatewayURL, "/"), nil
	}

	// Try to look up the gateway Service.
	for _, ns := range []string{"routines-system", globals.namespace} {
		var svc corev1.Service
		if err := c.Get(ctx, types.NamespacedName{
			Namespace: ns, Name: "routines-gateway",
		}, &svc); err == nil {
			port := int32(8080)
			for _, p := range svc.Spec.Ports {
				if p.Name == "http" || p.Port == 8080 {
					port = p.Port
					break
				}
			}
			return fmt.Sprintf("http://%s.%s.svc.cluster.local:%d",
				svc.Name, svc.Namespace, port), nil
		}
	}

	return "", fmt.Errorf("cannot determine Gateway URL; set --gateway-url or ROUTINES_GATEWAY_URL")
}

func newK8sClient() (client.Client, error) {
	scheme := runtime.NewScheme()
	if err := routinesv1alpha1.AddToScheme(scheme); err != nil {
		return nil, err
	}
	if err := corev1.AddToScheme(scheme); err != nil {
		return nil, err
	}

	cfg, err := buildRESTConfig()
	if err != nil {
		return nil, err
	}
	return client.New(cfg, client.Options{Scheme: scheme})
}

func buildRESTConfig() (*rest.Config, error) {
	loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
	if globals.kubeconfig != "" {
		loadingRules.ExplicitPath = globals.kubeconfig
	}
	return clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		loadingRules,
		&clientcmd.ConfigOverrides{},
	).ClientConfig()
}

func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// newUUID generates a simple time-based pseudo-UUID for message delivery IDs.
// In production, prefer github.com/google/uuid, but we avoid new deps.
func newUUID() string {
	return fmt.Sprintf("%x-%x", time.Now().UnixNano(), os.Getpid())
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
