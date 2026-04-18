# Routines

**Self-hosted, K8s-native, session-resumable Claude Routines.**

Routines is a Kubernetes operator that lets you schedule and trigger AI agents (Claude Code) using familiar K8s primitives. Define a `Routine` CR, attach a trigger (cron schedule, webhook, or GitHub event), and the operator handles agent lifecycle, PVC persistence, and credential injection.

## Quick Start

### Prerequisites

- Go v1.24+
- Docker 17.03+
- kubectl v1.19+
- A Kubernetes cluster (local: kind or k3d works great)
- Helm v3

### 1. Install with Helm

```sh
# Add the chart (once published)
helm install routines oci://ghcr.io/a2d2-dev/charts/routines \
  --namespace routines-system --create-namespace

# Or install from source:
make install      # install CRDs
make deploy IMG=ghcr.io/a2d2-dev/routines:latest
```

### 2. Create your first Routine

```yaml
# daily-standup.yaml
apiVersion: routines.a2d2.dev/v1alpha1
kind: ScheduleTrigger
metadata:
  name: every-morning
spec:
  schedule: "0 9 * * 1-5"   # 09:00 UTC, Mon–Fri
---
apiVersion: routines.a2d2.dev/v1alpha1
kind: Routine
metadata:
  name: daily-standup
spec:
  prompt:
    inline: |
      Check the open GitHub issues labeled "needs-triage", summarize them,
      and post a comment on each with suggested next steps.
  triggerRefs:
    - kind: ScheduleTrigger
      name: every-morning
  maxDurationSeconds: 600
```

```sh
kubectl apply -f daily-standup.yaml
```

### 3. Send a one-off message with the CLI

```sh
# Build the CLI
make build-cli

# List routines in the current namespace
./bin/routines list

# Send a manual prompt to the running agent
./bin/routines msg daily-standup "Also check PRs opened in the last 24h"

# View history
./bin/routines history daily-standup

# Stream agent logs
./bin/routines logs daily-standup --follow

# Suspend / resume
./bin/routines suspend daily-standup
./bin/routines resume daily-standup
```

## CLI Reference

```
Usage:
  routines [command]

Available Commands:
  list        List Routine CRs in the current namespace
  describe    Show Routine details and recent message history
  msg         Enqueue a message to a Routine via the Gateway
  history     Show Gateway message history for a Routine
  logs        Stream or print agent pod logs for a Routine
  suspend     Suspend a Routine (scales agent pod to zero, retains PVC)
  resume      Resume a suspended Routine

Global Flags:
  --kubeconfig string       Path to kubeconfig (default: $KUBECONFIG)
  -n, --namespace string    Kubernetes namespace (default: "default")
  --gateway-url string      Gateway base URL (auto-detected from K8s Service if empty)
```

## Architecture Overview

```
Trigger (Schedule/Webhook/GitHub)
         │
         ▼
    Gateway (HTTP)          ← /v1/enqueue
         │  PVC file queue
         ▼
  Agent Runtime Pod         ← lease / ack / nack
         │
         ▼
   Claude Code CLI          ← child process
         │
         ▼
   Output / PR / Comment
```

- **Controller** — reconciles `Routine` CRs, manages StatefulSet + PVC lifecycle.
- **Gateway** — HTTP server that queues messages and serves a lease/ack API.
- **Agent Runtime** — long-polls the Gateway, launches Claude Code per message.
- **CLI** — `kubectl`-style tool for human-in-the-loop interaction.

## Development

```sh
# Run all unit tests
make test

# Run integration tests (requires envtest binaries)
make test-integration

# Build all binaries
make build-all

# Run linter
make lint
```

## Contributing

Contributions welcome! Please open an issue first to discuss proposed changes.

## License

Copyright 2026. Licensed under the [Apache 2.0 License](LICENSE).
