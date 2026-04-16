---
stepsCompleted: [1, 2, 3, 4, 5, 6, 7, 8]
inputDocuments:
  - '_bmad-output/prd.md'
workflowType: 'architecture'
lastStep: 8
project_name: 'Routines'
user_name: 'Neov'
date: '2026-04-17'
revision: 1
---

# Architecture Decision Document

## 1. Overview

Routines 是 K8s 自托管的 Claude Routines 开源实现。运行时由三层组件组成，控制平面与消息流**正交**。

```
cron / webhook / GitHub event
             │
             ▼
┌───────────────────────────────────────────┐
│  Gateway  (集群共享 Deployment)            │
│  - 统一接收所有输入源                       │
│  - 文件队列 (PVC 上的目录 + JSONL)         │
│  - HTTP API: /enqueue /lease /ack /nack    │
└──────────┬────────────────────────────────┘
           │ HTTP (pull)
     ┌─────┴─────┐
     │           │
┌────▼──────┐ ┌──▼────────┐   每个 Routine 一个 Pod
│Routine-A  │ │Routine-B  │   Pod 内:
│  Pod      │ │  Pod      │     - Agent Runtime (main container)
└───────────┘ └───────────┘       - fork Claude Code 做一次消息
```

| 层 | 部署形态 | 职责 |
|---|---|---|
| **Gateway** | 集群共享 Deployment + 1 PVC | 归一化输入 + 持久队列 + HTTP API |
| **Agent Runtime** | 每个 Routine Pod 的 main container（我们的 Go 二进制） | pull 消息 → 编排 CC → 管 session / 凭据 / 观测 / 故障 |
| **Claude Code** | Agent Runtime 的 child process | 读 prompt、调工具、退出 |
| **Controller** (控制平面) | Deployment (leader election) | 管 Routine CR → Pod / PVC 生命周期，**不碰消息** |

---

## 2. Gateway

### 2.1 职责

- 接收三种输入源: cron / webhook / GitHub event
- 验签、幂等检查、CEL 过滤、归一化为标准 Message
- 把 Message 写入对应 Routine 的文件队列
- 提供 lease / ack / nack HTTP API
- 维护每个 Routine 的 `events.jsonl` 审计流和 session-id 元数据

### 2.2 文件队列布局（PVC `/data/`）

对齐 `~/.claude` 的设计哲学 —— 一切是文件，append-only JSONL + 原子 rename。

```
/data/
├── queues/
│   └── <routine-uid>/             ← 每个 Routine 独立目录
│       ├── inbox/                 ← 待 lease
│       │   └── <ts>-<deliveryID>.json
│       ├── processing/            ← 已 lease，等 ack (lease-timeout 后回 inbox)
│       ├── done/                  ← 成功完成
│       └── failed/                ← nack / 超时
├── sessions/
│   └── <routine-uid>/
│       └── meta.json              ← 当前 claude-session-id 等
└── events/
    └── <routine-uid>.jsonl        ← append-only 审计流
                                      (enqueued / leased / acked / failed …)
```

消息迁移全靠 POSIX `rename()` 的原子性，无需额外锁。

### 2.3 HTTP API

| Method | Path | 用途 |
|---|---|---|
| `POST` | `/v1/enqueue` | 内部触发源入队（schedule goroutine 用） |
| `POST` | `/webhooks/<trigger-name>` | 外部 webhook 入口 |
| `POST` | `/webhooks/github/<installation>` | GitHub App callback |
| `GET`  | `/v1/lease/<routine-uid>?wait=30s` | Agent 长轮询取消息 |
| `POST` | `/v1/ack/<routine-uid>/<messageID>` | Agent 报告成功（带 exit code / 耗时 / token 用量） |
| `POST` | `/v1/nack/<routine-uid>/<messageID>` | Agent 报告失败（带 reason / retryable / backoff） |
| `POST` | `/v1/heartbeat/<routine-uid>` | Agent 周期心跳（Pod alive / 当前 message / 资源占用） |
| `GET`  | `/v1/history/<routine-uid>?since=` | 查询运行历史 |

### 2.4 部署

- Deployment，`replicas: 2+`，无状态
- 挂 1 个 RWO PVC 在 `/data/`（只 leader 可写，follower standby）
- Leader election（客户端锁或 K8s lease）：只 leader 执行 cron / watchdog
- Webhook ingress 和 cron 调度在同一 binary，不同 HTTP / goroutine 入口

### 2.5 Trigger 实现

| Trigger | 实现 |
|---|---|
| `ScheduleTrigger` | Gateway leader goroutine 内部 cron 调度，到点 `/v1/enqueue` |
| `WebhookTrigger` | Gateway HTTP 路由 `/webhooks/<name>`，验签 → enqueue |
| `GitHubTrigger` | Gateway 订阅 GitHub App webhook，路由 + CEL → enqueue |

所有 trigger 最终都归到同一个 enqueue 路径，queue 以后只认 `Message` 这一个结构。

---

## 3. Agent Runtime

Agent Runtime 是每个 Routine Pod 里的 **main container**，一个 Go 二进制（`routines-agent`）。**不是一个简单的轮询器**，是 Pod 内的消息编排层。

### 3.1 职责

| 职责域 | 做什么 |
|---|---|
| **拉取调度** | 心跳 pull（30s 长轮询） / lease / ack / nack 状态机；同一 session 内 FIFO 严格串行 |
| **工作区管理** | git clone / fetch / checkout；每条消息前 clean workdir；完成后清理 |
| **CC 生命周期** | 构造 argv + env + cwd 拉起 `claude` 子进程；session-id 持久化 → 下条消息 `--resume`；超时 SIGTERM → SIGKILL grace；exit code / stderr 捕获 |
| **凭据注入** | 从 Pod env（由 Controller 按 ConnectorBinding 渲染）组装 Anthropic / Git / Connector 凭据，透传给 CC 子进程；日志脱敏 |
| **观测 + 审计** | 周期心跳上报；exit code / 耗时 / token usage 一起 ack；CC 日志按 messageID 归档到 `/work/logs/` |
| **故障保护** | 瞬时失败 backoff + nack；持续失败让 Pod 状态退化让 Controller 发现；Gateway 不可达本地缓存，不无脑重试；PVC 水位守护 |

### 3.2 包装形态

| 阶段 | 形态 | 理由 |
|---|---|---|
| **MVP** | Agent Runtime 作为 main container，CC 作为它的 child process | 简单；共享 env/cwd/fd；一个 image 一个 entrypoint |
| **Phase 2+** | Agent Runtime 作为 sidecar，CC 独立 main container，共享 `/work` volume | 需要把 CC 的网络 / FS 权限用独立 container 隔离时 |

Agent Runtime 代码在两种形态下**一致**，区别只在 CC 的 fork 方式（child process vs `exec` 进另一个 container）。

### 3.3 Pod 内文件布局（PVC `/work/`）

```
/work/
├── routine/
│   ├── prompt.md          ← Controller 从 Routine.spec.prompt 渲染写入
│   └── config.json        ← connector 映射、env 渲染规则
├── repo/                  ← git checkout 工作目录
├── sessions/
│   └── <claude-session-uuid>.jsonl  ← Claude Code 自己维护，对齐 ~/.claude/projects
├── session-id             ← 当前 claude-session-id
├── logs/
│   └── <messageID>/
│       ├── stdout.log
│       └── stderr.log
└── outputs/               ← 可选，CC 在 prompt 指导下写入的摘要
```

### 3.4 一次消息的处理流程（MVP oneshot 策略）

```
1. GET /v1/lease/<routine-uid>?wait=30s   (Gateway 返回 message 或 204)
2. 清理 repo 到 clean 状态 + git fetch
3. 组装 CC env:
      ANTHROPIC_BASE_URL / MODEL / AUTH_TOKEN  (from ConnectorBinding 渲染)
      GH_TOKEN / LINEAR_API_KEY / ...           (其他 Connector)
4. 拉起:
      claude -p "$(cat /work/routine/prompt.md)

                 ---INPUT---
                 $(cat msg.payload)" \
             --resume "$(cat /work/session-id || new-uuid)"
5. 等待退出（受 Routine.spec.maxDurationSeconds 控制）
6. 捕获 exit code / runtime / token usage（从 CC stdout 解析）
7. POST /v1/ack 或 /v1/nack
8. goto 1
```

退出码约定：
- `0` = Succeeded → ack
- 非 0 = Failed → nack（retryable=false）
- `75 (EX_TEMPFAIL)` = Resumable → ack 但保留 session，让下条消息续聊

---

## 4. Claude Code

### 4.1 Sandbox Image

- 默认：`ghcr.io/a2d2-dev/sandbox:latest`（Helm value `agentImage` 可覆盖）
- Image 内容：
  - Debian/Alpine base
  - `routines-agent` Go 二进制（ENTRYPOINT）
  - Claude Code CLI
  - git / gh / jq / curl 等基础工具
- 用户可自构建 image（加工具、pin CC 版本），只要保留 `/usr/local/bin/routines-agent` 作为入口即可

### 4.2 角色定位

CC 是**被编排的执行器**，不是我们的抽象层：
- 只做：读 prompt → 调 LLM → 调工具 → 写产物 → 退出
- 每条消息一个新 CC 进程（MVP oneshot 策略）
- session 恢复通过 `--resume <uuid>` 机制，state 在 `/work/sessions/`
- Routines 不解析 CC 的输出结构，只看 exit code + 从 stdout 正则抽 token usage

---

## 5. 控制平面（Controller + CRDs）

### 5.1 CRD 一览

**3 类 CRD**（Trigger 三种是独立 kind）：

| CRD | 谁写 | 谁 watch | 作用 |
|---|---|---|---|
| `Routine` | Dev | Controller | 声明"要一个 AI 员工" |
| `ScheduleTrigger` / `WebhookTrigger` / `GitHubTrigger` | Dev | Gateway | 声明输入源 + 绑哪些 Routine |
| `ConnectorBinding` | 平台管理员 | Controller（用于渲染 Pod env） | 把 Secret 绑到 named connector |

**没有 `RoutineRun` CRD**。运行历史在 Gateway 的 `queues/done/` + `events.jsonl`，不进 etcd。

### 5.2 Controller 职责

Controller 只做 K8s 对象生命周期管理，**不碰消息**：

```
Routine CR 创建
  → Controller 创建:
      - StatefulSet (replicas=1, ownerRef=Routine)
        Pod spec:
          image: <helm-value agentImage>
          env:  <从 ConnectorBindingRefs 渲染>
          volumeMounts: /work (PVC)
      - PVC (ownerRef=Routine)
  → Controller 通知 Gateway: 新 Routine 注册，准备 queue dir

Routine CR 更新 (prompt / connector / suspend)
  → Controller rollout StatefulSet
  → suspend=true: 缩容 replicas=0（Pod 停，PVC 保留）

Routine CR 删除
  → Finalizer 保护 PVC：要求用户显式 --force-keep-pvc=false 才 GC
     (会话历史是用户几个月的 AI 同事记忆，不能误删)
```

Controller 和 Gateway 之间的唯一协作：Routine 创建 / 删除时通知 Gateway 注册 / 注销 queue dir。走 Gateway 的内部 HTTP API，不走 K8s API。

### 5.3 CRD v1alpha1 字段草案

**Routine**
```yaml
apiVersion: routines.a2d2.dev/v1alpha1
kind: Routine
metadata:
  name: nightly-issue-triage
  namespace: team-backend
spec:
  prompt:
    inline: |
      Look at the latest 5 open issues in linear...
  repositoryRef:
    name: company-backend-repo
  connectorBindingRefs:
    - name: github-writer
    - name: linear-reader
    - name: anthropic-credentials
  triggerRefs:
    - { kind: ScheduleTrigger, name: nightly-2am }
  maxDurationSeconds: 1800
  suspend: false
status:
  phase: Ready | Suspended | Terminating
  podReady: true
  currentMessageID: <opt>
  lastMessageAt: <ts>
  gatewayRegistered: true
```

**ScheduleTrigger**
```yaml
apiVersion: routines.a2d2.dev/v1alpha1
kind: ScheduleTrigger
metadata: { name: nightly-2am }
spec:
  cron: "0 2 * * *"
  timezone: "Asia/Shanghai"
  routineRefs:
    - { name: nightly-issue-triage, namespace: team-backend }
```

**WebhookTrigger**
```yaml
apiVersion: routines.a2d2.dev/v1alpha1
kind: WebhookTrigger
metadata: { name: alertmanager-webhook }
spec:
  signatureScheme: hmac        # hmac | bearer
  secretRef: { name: alertmanager-signing-secret }
  routineRefs:
    - { name: alert-triage, namespace: team-sre }
status:
  publicURL: https://routines.example.com/webhooks/alertmanager-webhook
```

**GitHubTrigger**
```yaml
apiVersion: routines.a2d2.dev/v1alpha1
kind: GitHubTrigger
metadata: { name: pr-security-review }
spec:
  installationRef: { name: a2d2-github-app }
  repositories:
    - { owner: acme, name: backend }
  events: [pull_request]
  filter: |
    event.action in ["opened", "synchronize"] &&
    event.payload.pull_request.draft == false
  routineRefs:
    - { name: pr-reviewer, namespace: team-backend }
```

**ConnectorBinding**
```yaml
apiVersion: routines.a2d2.dev/v1alpha1
kind: ConnectorBinding
metadata:
  name: anthropic-credentials
  namespace: team-backend
spec:
  secretRef: { name: anthropic-secret }
  scope: readonly              # readonly | readwrite
  inject:                      # 规则列表
    - { as: env, key: API_KEY,  envName: ANTHROPIC_AUTH_TOKEN }
    - { as: env, key: BASE_URL, envName: ANTHROPIC_BASE_URL }
    - { as: env, key: MODEL,    envName: ANTHROPIC_MODEL }
```

---

## 6. 端到端消息生命周期

```
┌──────────────┐
│ Trigger 触发 │  Schedule (Gateway cron goroutine)
└──────┬───────┘  Webhook   (外部 POST /webhooks/<name>)
       │          GitHub    (GitHub App callback)
       ▼
┌─────────────────────────────────────┐
│ Gateway 处理                         │
│  - 验签 / 幂等 / CEL 过滤            │
│  - 找到对应 Routine(s)              │
│  - 为每个 Routine:                  │
│      ├ 生成 messageID                │
│      ├ write queues/<r>/inbox/<m>   │
│      └ append events/<r>.jsonl      │
└─────────────────┬───────────────────┘
                  │
                  ▼
┌─────────────────────────────────────┐
│ Agent Runtime pull                   │
│  - GET /v1/lease/<r>?wait=30s       │
│  - Gateway: rename inbox→processing │
│  - 返回 message + session-id        │
└─────────────────┬───────────────────┘
                  │
                  ▼
┌─────────────────────────────────────┐
│ Agent Runtime 处理                   │
│  - 清理 repo + git fetch             │
│  - 组装 CC env（from ConnectorBinding│
│  - fork claude -p ... --resume …    │
│  - 等退出（超时强杀）                 │
│  - 捕获 exit/runtime/token          │
│  - 写 /work/logs/<msgID>/           │
└─────────────────┬───────────────────┘
                  │
                  ▼
┌─────────────────────────────────────┐
│ Agent Runtime ack                    │
│  - POST /v1/ack  (或 /v1/nack)      │
│  - Gateway: rename processing→done  │
│              (或 → failed)           │
│  - append events                     │
└──────────────────────────────────────┘

循环回到 lease。
```

**崩溃恢复：**
- **Pod 崩溃**：processing/ 中的消息在 lease timeout（默认 2×maxDurationSeconds）后由 Gateway 自动 rename 回 inbox/（或 failed/ 如果超过重试次数）
- **Gateway 崩溃**：Deployment replicas=2+，leader election 接管 cron / watchdog
- **PVC 损坏**：session 历史丢失（文档化说明）；repo 可从 git 恢复

---

## 7. 部署拓扑

| Workload | Kind | Replicas | PVC | 说明 |
|---|---|---|---|---|
| `routines-controller` | Deployment | 2 (leader election) | 无 | 管 CRD → runtime object |
| `routines-gateway` | Deployment | 2+ (1 leader) | 1 × RWO | 消息中枢 |
| `routines-<routine>` | StatefulSet（Controller 创建） | 1 | 1 × RWO per Routine | Agent Runtime + CC |

加上 Helm Chart：CRD / ServiceAccount / RBAC / Gateway Service / (可选) Ingress。

**无外部依赖**：不引入 NATS / Redis / PostgreSQL。Queue 用文件系统，状态在 K8s + PVC。

---

## 8. 关键设计决策 & 理由

| 决策 | 为什么 |
|---|---|
| **Queue 放 Gateway 侧，不在每个 Routine Pod 侧** | 若 queue 在 Pod 侧，Gateway 必须 push 给 Pod，要求 RWX PVC 或 Sidecar HTTP endpoint；Pod 主动 pull 最简单 |
| **文件系统 = Queue DB（第一阶段）** | 对齐 `~/.claude` 哲学；零外部依赖；POSIX `rename()` 原子性足够；Phase 2 需要时再换 NATS / Redis |
| **Pull 模式（心跳）** | 对齐 Paperclip；Pod 不需对外暴露网络；Gateway 也不需发现 Pod |
| **Pod = Agent Runtime + CC，不是"直接跑 CC"** | Agent Runtime 承担消息编排、session、凭据、工作区、故障保护；CC 只做 LLM 对话 |
| **MVP: Agent Runtime 作 main container，CC 是 child process** | 简单；共享 env/cwd/fd；Phase 2 可切到 sidecar 做隔离 |
| **只有 3 类 CRD，没有 RoutineRun** | RoutineRun 是"消息历史"，属于 Gateway 的数据，不是 K8s 对象；避免 etcd 被短生命对象充满 |
| **Controller 和消息流正交** | Controller 只管 K8s 对象生命周期；消息流全走 Gateway HTTP；failure 域隔离 |
| **Session 持久化复用 CC 的 `--resume`** | CC 已经做了，不重复发明 |
| **Routine 删除用 Finalizer 保护 PVC** | 会话历史是用户几个月的 AI 同事记忆，不能 `kubectl delete routine` 就抹掉 |
| **LLM 模型 / endpoint / token 全走 ConnectorBinding** | 不绑供应商；schema 永远不追 LLM 行业命名；换模型 = `kubectl edit secret` |

---

## 9. 技术栈

| 层 | 技术 |
|---|---|
| Controller | Go + controller-runtime + kubebuilder |
| Gateway | Go + stdlib HTTP + 文件系统 |
| Agent Runtime | Go（作为 sandbox image 的 entrypoint） |
| Claude Code | 官方 CLI，由 sandbox image 打包 |
| Storage | K8s PVC（本地文件系统） |
| External deps | **无**（不用 NATS / Redis / PostgreSQL / 任何外部服务） |

---

## 10. Phase 2+ 未决项

- **Session rollover**：context 溢出时让 CC 自己总结 + 开新 session
- **CC 策略扩展**：long-running / batch 模式（目前只有 oneshot）
- **Sidecar 隔离模式**：CC 独立 container，Agent Runtime 作为 sidecar
- **RWX PVC 支持**：Gateway 直挂 Routine PVC 的高级场景
- **外部 MQ 替换**：高吞吐时文件队列换 NATS JetStream
- **Metrics**：Gateway 和 Agent Runtime 暴露 `/metrics`（Prometheus）
- **Web Dashboard**：只读
- **跨集群 / 多租户强隔离**
- **Token / USD 预算 sidecar**（LLM 计费可观测性）

---

## 11. 与 PRD 的偏离（需同步）

本架构与当前 PRD (`_bmad-output/prd.md` revision 3) 有几处偏离，后续需同步更新 PRD：

| PRD 位置 | PRD 原表述 | 架构实际 | 偏离原因 |
|---|---|---|---|
| PRD §API Surface `RoutineRun` | 每次触发一个 CR | **没有 RoutineRun CRD**；Gateway queue/events 承载 | etcd 不该装短生命对象；文件队列更自然 |
| PRD FR12–17 (Run Execution & Lifecycle) | 描述 RoutineRun 对象的状态机 | 重构为 "Gateway message 状态机（inbox/processing/done/failed）" + "Pod 心跳 pull" | 新架构下 RoutineRun 不再是一等对象 |
| PRD FR13a (Run 可恢复 / continue) | "对已完成 Run 发起 continue" | "向 Gateway enqueue 一条新消息（trigger = on-demand）" | 语义更自然：Routine 是常驻员工，continue 就是发条新指令 |
| PRD `RoutineRun` 相关 CLI | `kubectl routines continue <run>` | `routines msg <routine> "<text>"` 或 `routines run now <routine>` | 对应上面 |
| PRD §"基础设施层" | 未提及 Gateway | 引入 Gateway 作为中枢组件 | 消息归一 + 文件队列必须有一个中心 |

PRD 这几处改动会在下一个 revision 统一做，不影响当前架构的评审。
