---
stepsCompleted: [1, 2, 3, 4, 6, 7, 8, 9, 10, 11]
inputDocuments: ['https://code.claude.com/docs/en/routines (upstream reference)']
documentCounts:
  briefs: 0
  research: 1
  brainstorming: 0
  projectDocs: 0
workflowType: 'prd'
lastStep: 11
skippedSteps: [5]
status: 'complete'
project_name: 'Routines'
user_name: 'Neov'
date: '2026-04-16'
mode: 'yolo'
revision: 4
revisionNotes: 'Revision 4 (2026-04-17): align PRD with canonical architecture (`_bmad-output/architecture.md`, commit b0ec405). Key shifts: (1) drop the `RoutineRun` CRD — message history lives in Gateway file queue (`queues/<routine-uid>/{inbox,processing,done,failed}/` + `events.jsonl` on PVC), not in etcd; (2) `Routine` is a persistent "AI employee" Pod (StatefulSet replicas=1 + PVC, long-lived), not a template that spawns per-Run Pods; each Message = one new `claude` child process, session continuity via `claude --resume`; (3) introduce Gateway as the message hub (cluster-shared Deployment + PVC, HTTP API `/enqueue` `/lease` `/ack` `/nack` `/heartbeat` `/history` `/webhooks/*`) — the unique bridge between Trigger CRs and Routine Pods; (4) CLI: `routines continue <run>` → `routines msg <routine> "<text>"` (enqueue a message to an existing AI employee); add `routines history <routine>` for Gateway queue inspection; (5) MVP Gateway `replicas: 1` (RWO PVC, no HA); HA (RWX PVC or external MQ) deferred to Phase 2. FR12–17 rewritten into "Routine Pod Lifecycle" + "Message Lifecycle (Gateway)" + "Message-Level Session Continuity"; downstream FRs + NFRs updated in place. Executive Summary, Success Criteria, MVP boundary and Innovation positioning preserved — only data-model and lifecycle semantics tracked to the canonical architecture.'
---

# Product Requirements Document - Routines

**Author:** Neov
**Date:** 2026-04-16

---

## Executive Summary

### 产品愿景

**Routines 是 [Claude Routines](https://code.claude.com/docs/en/routines) 的 Kubernetes 自托管开源版。**

上游 Anthropic Claude Routines 是 SaaS：必须绑 claude.ai 账号、跑在 Anthropic 基础设施上、按它的环境模型来组织。Routines 把同样的能力（cron / webhook / GitHub 事件触发 → Claude Code 自动跑一段任务 → 提 PR / 写评论 / 发 Slack）做成一组**标准 K8s CRD + Operator**，让你在自己的集群里跑，复用集群已有的 RBAC、NetworkPolicy、Quota、GitOps、Secret 管理。

你定义一次 Prompt、一个代码仓库、几个连接器、一组触发器 —— 然后 Routines Controller 在你的集群里为这个 Routine 拉起一个**常驻 Pod（StatefulSet, replicas=1 + PVC）**作为"长期在岗的 AI 同事"。一个集群共享的 **Gateway** 把三种触发源（cron / webhook / GitHub event）统一归一化为消息入队，Routine Pod 按心跳从 Gateway 拉消息、逐条 fork `claude` 子进程处理；每条消息的 audit 事件、产物链接（commit / PR / Slack 消息）持久化在 Gateway 文件队列与 Routine PVC 上，`kubectl describe routine` / `routines history <routine>` 即可回看。

### 解决的核心问题

当前 AI 编程助手有一个根本局限：**交互是人发起的**。人不动，AI 不动。让 AI"自动值夜班"目前有三条路，每条都难走：

- **Anthropic Claude Routines（SaaS）**：能用，但是闭源、绑账号、跑在厂商基础设施上。代码不出公司网络的团队、有合规审计要求的团队没法用。
- **Claude Code 本地 `/schedule`**：必须本地开着电脑。合上盖子，定时任务全死。
- **GitHub Actions / n8n + LLM 节点**：调度器不理解 "prompt + repo + connector + 会话状态"，需要自己写一堆胶水。

Routines 把 AI Agent 从"桌面工具 / 厂商 SaaS"升级为**集群里的常驻同事**，在你已经信任的 K8s 边界内工作。

### 目标用户

- **熟悉 Claude Routines、想搬到自己集群里的开发者** —— 上游 SaaS 不能用（合规 / 网络 / 主权），但要的就是同样这个能力；
- **平台工程 / SRE 团队** —— 想把 AI Agent 嵌入 CI/CD、告警响应、合规巡检等既有工作流，且要复用 K8s 现有工具链；
- **拥有自建 K8s 的中小团队** —— 用开源方案补充 / 替代闭源托管服务；
- **企业混合部署诉求** —— LLM endpoint 想换就换（直连 Anthropic / Bedrock / Azure / 本地兼容端点），通过标准 Secret 注入，Routines 本身不绑特定模型供应商。

### What Makes This Special

**1. 一个不再"必须开着笔记本"的 Claude Routines**
跑在 K8s 集群里，运行节奏由集群决定不由你的电脑决定。这是上游 Claude Routines 的 SaaS 版能给的，本地 `/schedule` 给不了；现在自托管也能拿到了。

**2. K8s 原生，而不是"跑在 K8s 上"**
Routines 不是一个容器化的后端服务，而是一组**自定义资源 (CRD) + Operator + Gateway**。`Routine`、`ScheduleTrigger` / `WebhookTrigger` / `GitHubTrigger`、`ConnectorBinding` 都是一等 K8s 对象，可以 `kubectl apply`、被 GitOps 管理、被 RBAC 控制、被 ArgoCD 同步。**消息流（短生命周期的触发消息与运行历史）不进 etcd**，由集群内一个共享 Gateway 的文件队列承载 —— 控制平面只管"声明",消息流只管"运行时"，两者正交。工程师熟悉的所有 K8s 工具链立即可用。

**3. 三种触发方式即插即用**
定时（cron）、Webhook、GitHub 事件三条触发路径原生集成，在同一个 `Routine` 上可叠加使用。不需要用户自己搭 Jenkins / GitHub Actions / n8n 的胶水层。

**4. 每个 Routine 是一个常驻 AI 同事**
Routine 不是"一次性 Job"。每个 Routine = 一个 StatefulSet(replicas=1) + PVC，像团队里一个长期在岗的员工。消息按心跳拉取、逐条处理；Claude Code session 通过 `--resume` 机制在同一 PVC 上延续。用户随时可以 `routines msg <routine> "<text>"` 给它补一条指令 —— 从交互 IDE 的"session 续聊"升级为集群里一个**常驻角色的任务流**。

**5. 可观测、可审计、可重放**
每条 Message 都有完整审计：Gateway 的文件队列（`inbox/processing/done/failed`）+ `events.jsonl` 记录入队 / 领取 / ack / nack 的全流水。`kubectl describe routine` 可看最近 N 条消息摘要与 Pod 健康，`routines history <routine>` 查 Gateway 流水；产物链接（PR URL / commit SHA / Slack ts）写入 Routine PVC 的 `logs/<messageID>/` 与 `outputs/`，支持按消息回放和 diff。

## Project Classification

**Technical Type:** developer_tool（Kubernetes Operator + OSS 平台）
**Domain:** general（开发者效率 / DevOps 自动化）
**Complexity:** medium（K8s Operator 工程复杂度中等，无受监管领域合规负担）
**Project Context:** Greenfield — 新项目，无遗留系统

**核心技术栈倾向（已由 `_bmad-output/architecture.md` 锁定，commit b0ec405）：**
- **语言：** Go（与 K8s 生态一致，controller-runtime / kubebuilder）
- **形态：** 三层运行时（Gateway / Agent Runtime / Claude Code）+ 控制平面 Controller，参见 architecture §1：
  - **Gateway**（集群共享 Deployment + RWO PVC）：三种触发源入口 + 文件队列 + HTTP API (`/enqueue` `/lease` `/ack` `/nack` `/heartbeat` `/history` `/webhooks/*`)
  - **Agent Runtime**（每个 Routine Pod 的 main container，Go 二进制 `routines-agent`）：心跳 pull → 编排 Claude Code → 管 session / 凭据 / 观测
  - **Claude Code**（Agent Runtime 的 child process）：读 prompt、调工具、退出；每条消息一个新进程，session 续聊通过 `--resume <uuid>`
  - **Controller**（Deployment，leader election）：管 Routine CR → StatefulSet / PVC 生命周期，**不碰消息**
- **API 形态：** CRD（控制平面）+ HTTP（Gateway — 内部 enqueue 与 webhook 接入）
- **AI 执行：** 每个 Routine 对应一个 **StatefulSet (replicas=1) + 专属 PVC** 的常驻 Pod（长生存），按心跳从 Gateway 拉消息，每条消息 fork 一个新的 `claude` 子进程
- **存储：** K8s etcd（仅承载 `Routine` / 三种 Trigger / `ConnectorBinding` 的声明状态）+ Gateway PVC（文件队列 + 全局审计事件流）+ 每 Routine 一个 PVC（repo 工作区 + Claude Code session state + 消息日志 + 产物）+ 可选对象存储（归档）
- **外部依赖：** **无**（不引入 NATS / Redis / PostgreSQL；消息队列与审计流全部用 POSIX `rename()` 原子性 + append-only JSONL 承载）
- **环境模型：** 直接使用 K8s **Namespace** 作为"环境"边界（网络策略 / Secret / ConfigMap / Quota / LimitRange），不额外引入 Environment CRD

---

## Success Criteria

### User Success

对开发者来说，Routines **"成功"** 的判定标准不是"UI 好看"或"功能多"，而是以下几个可量化瞬间是否发生：

- **U1 · 5 分钟上手**：一个熟悉 K8s 的开发者能在 5 分钟内从 `helm install` 到跑起第一个 Routine（例如一个"每日凌晨 2 点扫 Issue"的示例）。
- **U2 · 一晚上见成果**：当用户为"积压清理"场景配置一个 Routine 后，**隔天早上醒来能在 GitHub 上看到 Routine 创建的真实 Draft PR**，且这些 PR 是可 review 而不是噪音。
- **U3 · "迁移无痛感"**：熟悉 Claude Code `/schedule` 或上游 Claude Routines 的用户，可以把定义几乎 1:1 迁移到 Routines（prompt + cron + repo），不需要重新学一套完整 DSL。
- **U4 · "我信任它夜里跑"**：用户愿意在没有人 review 每一步的情况下，允许 Routine 自动创建 PR/评论/Issue，因为 RBAC、审计、blast radius 控制到位。

### Business Success（开源项目视角）

"业务"对开源项目而言即**社区健康度与采纳度**：

- **B1 · 早期社区信号（v0.1 发布后 3 个月内）**：
  - GitHub ≥ 1,500 stars
  - ≥ 20 名外部贡献者提交过 merged PR
  - ≥ 5 个可公开引用的生产/半生产部署案例
- **B2 · 中期生态信号（v0.x 稳定 → v1.0，12 个月内）**：
  - ≥ 2 个第三方 Connector（Linear / Slack / Sentry / Jira 至少一个由社区贡献而非核心团队维护）
  - 出现在至少 1 个 CNCF sandbox / Landscape 相关讨论里
  - 在 "self-hosted Claude Routines alternative" 的搜索结果里稳定出现在首页
- **B3 · 长期愿景信号**：
  - 成为"K8s 上跑 AI Agent"场景的**默认选型之一**，在搜索"self-hosted Claude Routines alternative" / "K8s AI automation"时是首页结果。

### Technical Success

- **T1 · Operator 正确性**：Routine 控制器通过 kubebuilder 推荐的 conformance 测试集，在 3 个以上主流 K8s 发行版（上游 k8s、k3s、EKS）持续集成通过。
- **T2 · 幂等与可观测**：同一个触发事件在 Gateway 或 controller 重启/重放场景下**不会产生重复 Message**（以 `deliveryID` 为幂等 key）；每条 Message 可通过 `routines history <routine>` 或 Gateway `events.jsonl` 追溯状态机（`inbox → processing → done | failed`）与失败原因。
- **T3 · Blast Radius 控制**：默认配置下，任何一个 Routine Pod 不能：访问非声明的 Secret、写集群外资源、修改集群内 RBAC。这些限制有对应的单元/集成测试覆盖。

### Measurable Outcomes

| 维度 | 指标 | MVP 目标 | v1.0 目标 |
|---|---|---|---|
| 启动成本 | 从空集群到第一个 Routine 跑完 | ≤ 10 min | ≤ 5 min |
| 触发延迟 | cron 触发到 Pod Running 的中位值 | ≤ 30s | ≤ 10s |
| GitHub 事件到达率 | 非限流情况下从 webhook 入站到 Gateway 入队成功的比例 | ≥ 99% | ≥ 99.9% |
| 最小调度间隔 | 对 Schedule trigger 允许的最小 cron 间隔 | 10 分钟 | 10 分钟（社区策略可放宽） |
| Session 续聊成功率 | `routines msg <routine>` 对存活 Routine 的续聊成功率（消息被正确 ack 且 Claude Code `--resume` 加载既有 session） | ≥ 95% | ≥ 99% |
| Message 重复率 | 同一触发事件产生的重复 Message 比例（以 `deliveryID` 判定） | < 1% | < 0.1% |
| 集群规模 | 单集群同时存活的 Routine Pod 数（每 Routine 一个常驻 Pod） | ≥ 20 | ≥ 500 |
| 失败可诊断率 | `routines history <routine>` 或 `kubectl describe routine` 能直接定位失败 Message 原因的比例 | ≥ 90% | ≥ 99% |

## Product Scope

### MVP - Minimum Viable Product（v0.1 ~ v0.3）

目标：**"一个熟练 K8s 用户能在自己的集群里复现上游 Claude Routines 的核心用例"**。

**MVP 必须包含：**

- `Routine` CRD + Controller：定义 prompt / repo / connector bindings / triggers；Controller reconcile 时为每个 Routine 物化一个 **StatefulSet (replicas=1) + 专属 PVC** 常驻 Pod（Pod = Agent Runtime + Claude Code）。
- 三种 Trigger CRD + Gateway 订阅：`ScheduleTrigger`（Gateway leader goroutine cron）、`WebhookTrigger`（Gateway HTTP `/webhooks/<name>`，HMAC / Bearer 验签）、`GitHubTrigger`（GitHub App callback + CEL filter）。
- **Gateway**：集群共享 Deployment（MVP `replicas: 1` + 1 RWO PVC）+ HTTP API（`/v1/enqueue` `/v1/lease/<routine-uid>` `/v1/ack` `/v1/nack` `/v1/heartbeat` `/v1/history` `/webhooks/*`）+ 文件队列 (`/data/queues/<routine-uid>/{inbox,processing,done,failed}/` + `/data/events/<routine-uid>.jsonl`)。**控制平面与消息流的唯一分界**。
- **Message 级 session 续聊**：用户通过 `routines msg <routine> "<text>"`（或等价 HTTP `/v1/enqueue`）向现有 Routine 发送任意指令，Agent Runtime 拉到消息后调用 `claude --resume <session-id>` 续聊，Claude Code 在同一 PVC 读取既有 `~/.claude/projects/` session state 继续对话。
- `ConnectorBinding` CRD：基础 Git（读 + commit + push + 开 PR）连接器，使用 K8s Secret 注入凭据（同时承载 Anthropic 模型 / 端点 / token 的 env 注入）。
- GitHub trigger 基础过滤能力（通过 CEL 表达式，详见 FR8）。
- 一个最小 CLI / `kubectl` 插件（可选）：`routines list`、`routines run now <routine>`、`routines msg <routine> "<text>"`、`routines history <routine>`、`routines cancel <routine>`（取消当前 in-flight message）、`routines logs <routine>`、`routines playground` 等。
- Helm Chart 一键安装，内置 ServiceAccount / RBAC / CRD 安装 + Gateway Deployment + Gateway PVC；默认的 sandbox image（含 `routines-agent` ENTRYPOINT + Claude Code）作为 Helm value 暴露。
- 基础审计：Gateway `events.jsonl` 记录每条 Message 的入队 / lease / ack / nack + 退出码 / 耗时 / token usage；Routine PVC 的 `/work/logs/<messageID>/` 归档 Claude Code stdout/stderr；产物链接（PR URL / commit SHA）写入 `/work/outputs/` + Gateway 事件。

**MVP 明确不做：**

- 图形化 Dashboard（使用 kubectl + 日志平台）
- 多租户 quota / billing（这是 v1+ 议题）
- 非 GitHub 的事件源（GitLab / Bitbucket 等放 Phase 2）
- 图形化 Prompt IDE / 版本管理
- API token 的轮换 / 一次性显示 / 细粒度 revoke 流程（MVP 使用 K8s Secret 直接承载 bearer token，管理员自行 rotate）
- Agent 工具级权限精细控制（依赖 Namespace 的 RBAC / NetworkPolicy / Quota 作为唯一边界）
- **不抽象 Agent 运行时**：MVP 内置 Claude Code，sandbox image 由 Helm value 配置；想换其他 Agent 实现等到有真实社区 / 用户诉求时（Phase 2+）再设计抽象，避免过度设计
- **不在 Routine spec 里建模"模型"**：Claude Code 自己通过 `ANTHROPIC_BASE_URL` / `ANTHROPIC_MODEL` / `ANTHROPIC_AUTH_TOKEN` 等环境变量切换模型与端点，由 ConnectorBinding 注入；Routines schema 不引入 `model` 字段
- **不做 token / 美元预算**：Claude Code / Anthropic API 自己有计费可观测性；Routines MVP 只管 `maxDurationSeconds`（K8s 原生能强制的硬上限）

### Growth Features (Post-MVP, v0.4 ~ v0.9)

- **更多 Connector**：Linear、Slack、Jira、Sentry、Notion、Google Drive；
- **更多事件源**：GitLab、Bitbucket、通用 Webhook signature verification、Cloud Event 适配；
- **Run 资源 / 速率控制升级**：单 Routine 级别的 concurrencyPolicy、全局 rate limit、可选的 token / 美元预算 sidecar；
- **Routine 模板市场**：社区贡献的 `Routine` 模板（"每日 issue 分诊" / "PR 安全审查" / "文档同步" 等），可以像 Helm chart 一样被引用；
- **Web UI**：只读 Dashboard（列出 Routines、查看 Run 日志、手工 trigger），可选组件；
- **多集群管理**：一个 "control plane" Routine 把任务分发到多个 worker cluster；
- **Agent 抽象（按需引入）**：当出现真实需求要把 Claude Code 换成其他 Agent（Codex、Aider、SWE-agent、自研脚本）时，引入 `AgentProfile` CRD 作为 Routine spec 的可选 ref。**这是一个被刻意推迟的设计**：MVP 阶段没有任何用户验证过这个需求，先内置 Claude Code 跑通主流程。

### Vision (Future, v1.0+)

- **Agent Mesh**：多个 Routine 之间可以通过 K8s event 彼此编排，形成 DAG（一个 Routine 完成会触发下一个）；
- **Routine as a Product**：企业内部 Platform 团队可将 Routine 作为 "internal product" 发布给开发者团队，开发者通过简单表单即可订阅（Self-Service AI 自动化）；
- **Policy as Code**：OPA/Gatekeeper 集成，允许集群管理员以策略限定"哪些 Routine 可以跑 / 多大 blast radius / 可以调用哪些 Connector"；
- **SLM + 本地推理**：与 KServe / vLLM / Ollama Operator 集成（通过 Claude Code 的 `ANTHROPIC_BASE_URL` 指向本地兼容端点即可，Routines 本身无需改动）；
- **Hosted Trigger Hub（云端增值）**：核心项目保持 Apache-2.0 自托管开源；同时官方运营一个 **云端托管的 Trigger 聚合服务**——提供更频繁的 cron 粒度、官方托管的 GitHub App（不需要用户自己申请）、多源 Webhook 聚合、事件去抖与重放、可靠投递与 SLA。用户集群里跑的是相同的 OSS Operator，只是订阅了云端触发器；Routine 执行依然在用户自己的集群内完成。这一层是开源项目商业化的入口，但不是闭源门槛——自托管用户能力不受影响。

---

## User Journeys

### Journey 1：张浩 — 把"值夜班"交还给系统

**人物：** 张浩，一家 SaaS 创业公司的后端 Tech Lead，3 年 K8s 经验，团队 6 人。公司产品有一个老服务，每周平均有 3-5 个"非紧急 bug" 堆在 Linear 里没人排。张浩自己每晚 10 点后还会打开笔记本手动扫一遍 — 他很累。

**旧的世界：** 每天晚饭后他打开 Linear，看新上报的 bug，挑简单的手动修一修。周末补一补睡眠，但积压永远在涨。他尝试过 Claude Code 的 `/schedule`，问题是必须本地开着电脑，一合上盖子所有定时任务就死了。他也看了上游 Claude Routines，但公司代码不允许出公司网络。

**与 Routines 相遇：** 在 Hacker News 看到 "Self-hosted alternative to Claude Routines"。公司内网本来就跑着一个小 K8s 集群。

**安装与首跑：**

```bash
helm repo add routines https://routines.dev/charts
helm install routines routines/routines --namespace routines --create-namespace
kubectl apply -f first-routine.yaml
```

`first-routine.yaml` 里他写了：每天凌晨 2 点 → pull 公司 repo → Claude Code 跑一段 prompt（"扫 Linear 最新 5 个 bug，挑标为 `good-first-bug` 的尝试修复，在 `routines/*` 分支上提交并开 Draft PR"） → 使用 `github-writer` ConnectorBinding 开 PR。

**高潮时刻：** 第二天早上 9 点，他打开 GitHub，看到两个 Draft PR 挂在那，每个都带有修改说明、diff 解释和 test 运行结果。他花 20 分钟 review 了一下，一个直接合入，另一个他回到 CLI 执行 `routines msg nightly-issue-triage "PR #1234 的测试不够，请补充 table-driven 测试"` —— Gateway 把这条指令入队到 Routine 的 `inbox/`，常驻 Pod 在下一次心跳里 lease 到消息，Agent Runtime 以 `claude --resume <session-id>` 拉起新 `claude` 子进程，Claude Code 读取同一 PVC 上早上那次对话的 session state，往同一个分支补 commit。Routine 这个 "AI 同事" 一直在线，张浩只是又给它派了一个任务。

**新的世界：** 两周后，Linear 里的"good-first-bug"积压从 14 个降到 3 个，张浩晚上 10 点合上电脑就是真的合上。

**Journey 揭示的能力需求：**
- 一键 Helm 安装 + 内置 RBAC + Gateway Deployment；
- 声明式定义（cron + prompt + repo + connector）；
- Git 读写 ConnectorBinding + Issue Tracker ConnectorBinding；
- 默认分支前缀 `routines/*` 的 commit / 开 PR 能力；
- **Message 级 session 续聊**：Routine 是常驻 Pod，通过 `routines msg <routine> "<text>"` 向 Gateway enqueue 新消息，Agent Runtime `claude --resume` 在同一 PVC 上延续对话；
- Routine 运行结果的持久化（PR link、diff、Claude Code log、Gateway `events.jsonl` 审计流）。

### Journey 2：刘静 — 把告警交给 Routine 先看一遍

**人物：** 刘静，一家金融基础设施公司的 SRE，on-call 轮值组成员。她最讨厌的是"凌晨 3 点手机震动，打开电脑登 jump host，查日志，发现只是一个已知的 flaky test"。

**情景：** 团队使用 Sentry 收集生产错误。每个告警都会先 page 人。刘静想要一个"AI 先查一遍"层，只有真正严重的才 page 人。

**与 Routines 相遇：** 她的平台团队刚装了 Routines。她定义了一个 Routine：
- `trigger: webhook`（Sentry 通过 HTTP POST 直接打到集群入口）
- `prompt`：读告警 payload，在最近 48h 的 commit 里找可能关联点，运行 `git blame`，查 Sentry 历史事件频次，写一段 markdown 分析。
- `connector`：Sentry API（只读） + Slack（写某个 triage 频道）
- `policy`：不允许修改代码仓库（只读 repo）。

**高潮时刻：** 凌晨 3:17，Sentry 触发。1 分钟后 Slack 里出现一条结构化消息：`[AI Triage] Probably NOT urgent — same error rate last week, seems related to commit abc123 which is a known flaky integration test. Suggest snooze until morning.` 刘静没被吵醒。早上看 Slack 时她确认分析正确，点了 👍。

**Journey 揭示的能力需求：**
- `WebhookTrigger`：公网可达的 HTTP 端点 + 签名验证 + payload 转发进 Prompt 上下文；
- Connector 的**只读 / 只写 scope 限定**；
- Policy：一个 Routine 被限制只能使用声明的 Connector（不能越权访问其他 Secret）；
- 可审计的 Run 历史（Run#4532 的输入、prompt、输出、Slack 消息）。

### Journey 3：王磊 — Platform Engineer 维护 Routines 本身

**人物：** 王磊，一家中型电商的 Platform 工程师。他负责集群里所有"共享服务"的运维。老板要求他**给 Routines 做 rollout、升级、访问控制**，并且"不能让某个开发者写一个 Routine 把整个集群 RBAC 改坏"。

**日常工作流：**

1. **升级 Routines**：`helm upgrade routines routines/routines --version 0.5.2` — Routines 的 CRD 升级必须向后兼容，老的 `Routine` YAML 不能破坏。
2. **配额管理**：王磊通过 `ResourceQuota` 限定 `routines` namespace 最多同时跑 10 个 Routine Pod（每 Routine 一个常驻 Pod），避免 runaway cost。
3. **Connector 授权**：GitHub App 凭据只有王磊能创建，开发者只能 `connectorBindingRefs` 引用由王磊预先创建好的 Binding，不能自己写 token 进 YAML。
4. **故障排查**：某一天一个 Routine 陷入消息风暴（Trigger 每分钟入队一条），王磊 `routines history -A --since=1h` 看到异常流量，`kubectl delete scheduletrigger xxx`，cron 停，Gateway 的 `inbox/` 不再增长；可选地 `routines drain <routine>` 把剩余 in-flight 消息 nack 到 `failed/`。

**Journey 揭示的能力需求：**
- CRD 版本化 + conversion webhook；
- Namespace 级别的 ResourceQuota 生效（Routine Pod + Gateway Deployment 都受约束）；
- Connector 与 Routine 解耦，Routine 只能引用（`connectorBindingRefs`）；
- 标准 K8s 诊断命令友好（status subresource、printer columns）+ `routines history` 查 Gateway 消息流水；
- 暂停 / 停止机制（`spec.suspend: true` → StatefulSet 缩容到 replicas=0，PVC 保留）。

### Journey 4：API 消费者 — 监控系统主动触发 Routine

**人物：** 某监控系统（Prometheus Alertmanager）。它不是人，但它是 Routines 的 "API consumer"。

**流程：** Alertmanager 的 webhook 配置里写了一个 Routines 的 WebhookTrigger URL。告警触发时：

```
POST /webhooks/alertmanager-critical
Authorization: Bearer <signing-token>
Content-Type: application/json

{ ...alertmanager payload... }
```

Routines Gateway 收到请求后：
1. 验签 / 校验 token（HMAC 或 Bearer）；
2. 根据 URL 路径定位到对应的 `WebhookTrigger` CR；
3. 为该 Trigger 绑定的每个 Routine 入队一条 Message（`/data/queues/<routine-uid>/inbox/<ts>-<deliveryID>.json`）,payload 作为 `payload.text` 写入消息体；append `events.jsonl`；
4. 返回 `202 Accepted` + `messageID`，便于 Alertmanager 日志 / 集成测试追踪。

**Journey 揭示的能力需求：**
- 公网可达的 Gateway HTTP 入口（支持 IngressRoute / Gateway API）；
- 多种签名方案（HMAC、Bearer token、GitHub HMAC、Sentry HMAC）；
- 同步返回 `messageID` 句柄；
- 幂等 key（相同 `deliveryID` 不应在配置窗口内重复入队 Message）。

### Journey Requirements Summary

上述 4 个 Journey 揭示的能力域可归纳为：

1. **Routine 生命周期**：声明、版本化、暂停/恢复、删除、升级。
2. **触发能力**：cron 定时、入站 webhook（多签名方案）、GitHub 事件（原生）、API 手工触发 / 立即运行。
3. **Connector / Secret 解耦**：ConnectorBinding 与 Routine 分离，Routine 只能引用已授权的 binding。
4. **执行运行时**：每个 Routine 一个常驻 Pod（Pod 内 Agent Runtime + Claude Code），按心跳从 Gateway 拉消息并逐条处理；资源限制、单条消息超时、cancel 当前消息、每条消息的日志持久化；session 通过 `claude --resume` 在同一 PVC 上续聊。
5. **审计与可观测**：Gateway 消息流水查询、每条消息的 prompt / 输入 / 输出 / 产物 / Git commit / PR link；全局 `events.jsonl` + Routine PVC 上的 `logs/<messageID>/` 双层审计。
6. **Blast radius 控制**：Routine 不能超越声明的 Connector、不能访问未授权 Secret、不能写集群 RBAC。
7. **Dev Experience**：Helm 一键装、kubectl 友好、docs 与示例齐全、从上游 Claude Routines 迁移路径清晰。

---

## Innovation & Novel Patterns

### Detected Innovation Areas

Routines 的创新点不在于"更强的 AI"，而在于**把 Claude Routines 从厂商 SaaS 重新表达成一个 K8s 原语**。具体有三层：

**1. "Claude Routines as a Kubernetes-native primitive"（新范式）**

目前业界主流的 AI 自动化方案有两类：
- **SaaS 托管型**（Anthropic Claude Routines、v0、Devin 等）：闭源、vendor lock-in、跑在厂商基础设施上；
- **脚本/CI 型**（GitHub Actions + LLM 调用、n8n + OpenAI 节点等）：非原生 Agent 语义，调度器不理解 "prompt + repo + connector + 会话状态" 这个领域模型。

Routines 把 `Routine / ScheduleTrigger / WebhookTrigger / GitHubTrigger / ConnectorBinding` 沉淀成一等 K8s 对象，**并且用"常驻 Pod (StatefulSet replicas=1) + PVC + Claude Code --resume"承载"持续在岗的 AI 同事"这一新原语** —— 不是"serverless AI 函数"，而是一个长期存在、可反复被派发任务的员工。这是一个新的建模层次 —— 没人把 "Claude Routines 的领域模型 + 会话状态 + 常驻执行器" 同时建模；它和 Knative 把"事件驱动的 serverless 函数"变成 CRD 的做法类似，但对象是**有状态、长生存**的 Claude Code 任务流。

同时 Routines **刻意把消息流从 etcd 里剥出来** —— 由集群内一个共享 Gateway 的文件队列（POSIX `rename()` 原子性 + append-only JSONL）承载。这避免了 K8s 原生控制面常见的"短生命对象充满 etcd"问题，也让 Routines 本身**零外部依赖**（无 NATS / Redis / PostgreSQL），对齐 `~/.claude` "一切是文件" 的设计哲学。

**2. 基于表达式的 GitHub 事件过滤 + Playground 调试上下文**

Routines 的 GitHub trigger 过滤不采用闭源表单字段，而是使用 **CEL 表达式** 对一个标准化的 `event` 上下文对象求值。这带来两个好处：
- 表达式语言比 DSL 字段更灵活，`event.payload.pull_request.labels.exists(l, l.name == "needs-backport") && !event.payload.pull_request.draft` 这类复合条件天然可写；
- 官方提供一个 **Playground**（`kubectl routines playground <trigger>` 或云端 UI），用户贴入一份真实的 GitHub payload，实时看过滤表达式的求值结果——这是配置类 DSL 做不到的"调试体验"。

**3. 开源自托管 + 官方 Hosted Trigger Hub（双轨分工）**

核心 Operator 保持 Apache-2.0 开源自托管；官方同时提供一个云端 **Trigger Hub**：托管 GitHub App、聚合 webhook、提供比自托管更细粒度的 cron 和事件重放。自托管用户完全可用，订阅用户获得运维减负与 SLA。**这让开源项目不靠闭源功能也能有清晰的商业化路径**——实际执行依然在用户自己的集群里，只是触发源变"更省心"。

### Market Context & Competitive Landscape

| 方案 | 部署模式 | K8s 原生 | 可恢复会话 | 自托管模型/端点 | 开源 |
|---|---|---|---|---|---|
| Claude Code `/schedule` | 本地 CLI | 否 | 部分 | 是（env vars） | 否 |
| Anthropic Claude Routines (SaaS) | 闭源云端 | 否 | 是 | 否 | 否 |
| GitHub Actions + LLM | SaaS (GitHub) | 否 | 否 | 手写 | 部分 |
| n8n / Zapier + LLM 节点 | SaaS / self-host | 否 | 否 | 部分 | 部分 |
| **Routines (本项目)** | **Self-hosted K8s** | **是** | **是** | **是（Secret 注入）** | **是 (Apache-2.0)** |

Routines 的差异化可以用一句话概括：**"Self-hosted, K8s-native, session-resumable Claude Routines."** 这个交叉点目前没有占位者。

### Validation Approach

为了验证创新点真的解决用户问题而不是空中楼阁，MVP 阶段设置以下验证动作：

- **V1 · Dogfood**：核心团队自身使用 Routines 承担自动 issue 分诊 / PR 审查，作为最真实的长时间压力测试。
- **V2 · Design Partner 程序**：邀请 5 个不同规模的团队（独立开发者 / 平台团队 / 合规敏感组织）在内测期使用，每周收集反馈。
- **V3 · 上游迁移演示**：录制一个 5 分钟视频，把上游 Claude Routines 的一个真实任务定义迁移到自托管 Routines 上跑通，作为 README 的开场 demo。

### Risk Mitigation

| 风险 | 描述 | 缓解 |
|---|---|---|
| **Claude Code CLI 接口变化** | Anthropic 升级 Claude Code 可能改变 CLI 行为或 env vars 约定 | 用户自己控制 sandbox image 版本（Helm value），不被强制升级；CI 用 image digest pinning |
| **安全事件（被恶意 prompt injection 利用）** | Routine 被诱导执行越权操作 | 默认最小权限；Connector 白名单；输出需经策略检查 |
| **用户体验门槛过高** | "必须懂 K8s" 的门槛吓退大量潜在用户 | 提供 `routines` CLI 简化交互；提供 Helm + kind-based 快速 demo |
| **Runaway cost** | 某个 Routine 死循环消耗 LLM 预算 | 每个 Routine 必须声明 `maxDurationSeconds` 硬上限；token / 美元预算由 Anthropic console 一侧的 spend control 承担 |
| **上游 SaaS 改变定价 / 政策** | Anthropic 调整 Claude Routines 策略，让我们的差异化失效 | Routines 的核心价值是"自托管 + K8s 原生"，不是"复刻 SaaS 功能"；即便上游开放自托管，我们的 K8s 原生 + GitOps 心智仍有独立价值 |
| **开源项目动力不足** | 社区贡献、维护节奏掉队 | 早期清晰的 governance、CONTRIBUTING.md、明确的第三方 connector 路径 |

---

## Developer Tool Specific Requirements

> 本节对应 `project-types.csv` 中 `developer_tool` 的 key_questions：语言支持 / 包管理器 / 安装方式 / API surface / 示例 / 迁移指南。

### Project-Type Overview

Routines 的"产品界面"只有一层：**`kubectl apply -f routine.yaml`**。用户通过编辑 CRD YAML 来声明 Routine，全部交互沿用 K8s 工具链。

### Language Support Matrix

| 场景 | 语言 / 运行时 | 说明 |
|---|---|---|
| Core Operator / Controllers | Go 1.22+ | 与 K8s 生态一致，使用 controller-runtime / kubebuilder |
| Built-in Webhook Ingress | Go | 同核心二进制，避免额外进程 |
| 官方 CLI（可选） | Go | 作为 `kubectl-routines` plugin 发布 |
| Sandbox image | 任意（默认含 Claude Code CLI） | Helm value 暴露，可替换为自定义构建 |

### Installation Methods

MVP 必须支持以下安装路径：

1. **Helm**（官方主路径）：`helm install routines routines/routines`
2. **Plain YAML**（air-gapped）：`kubectl apply -f https://github.com/.../release.yaml`
3. **Kustomize overlay**（高级用户）：支持 base + overlay 组合
4. **kind/minikube quickstart**：一键脚本拉起本地集群 + Routines + demo Routine

**安装后的默认状态：**
- Routines 部署在自己的 namespace（默认 `routines-system`）
- 所有 CRD 已注册
- 一个默认的 ServiceAccount + RBAC（最小权限模板）
- 默认 sandbox image：`ghcr.io/a2d2-dev/sandbox:latest`（内置 Claude Code），可在 Helm values 中覆盖
- 一个 sample Routine（`routines-samples/hello-routine.yaml`），包含一次性 cron 触发，让用户 5 分钟内看到成功结果

### Environment Model — 直接复用 K8s Namespace

Routines **不引入独立的 "Environment" CRD**。每个 Routine 运行在某个 namespace 里，继承该 namespace 所有的原生能力：

| Routines 概念 | 由 K8s 原生对象承载 |
|---|---|
| 网络访问策略 | `NetworkPolicy`（也可与 Cilium / Calico 等 CNI 策略叠加） |
| 环境变量 / API key | `Secret` + `ConfigMap` + `ConnectorBinding` 引用 |
| 并发 / 资源上限 | `ResourceQuota` + `LimitRange` |
| RBAC 边界 | namespace 级 `Role` / `RoleBinding` |
| "环境隔离" | 一个 namespace = 一个环境（dev / staging / routines-trusted 等） |

**好处：**
- 零学习成本，管理员已有的 namespace 工具链直接可用；
- 不需要 Routines 自己重新实现一套 "Environment" 抽象；
- GitOps / ArgoCD 已有的 namespace-level sync 策略可以直接复用。

### LLM 模型 / 端点 / 凭据 — Routines 不建模

Routines schema 里**没有 `model` / `endpoint` / `apiKey` 字段**。这些都是 Claude Code 自己消费的信息，由用户通过 ConnectorBinding 把一个 K8s Secret 挂载到 Pod，Claude Code 读取标准环境变量决定行为：

| 配置项 | Claude Code 标准 env var | 在 Routines 里的来源 |
|---|---|---|
| 模型 | `ANTHROPIC_MODEL` | Secret key，或 prompt 内 `/model` 切换 |
| API endpoint | `ANTHROPIC_BASE_URL` | Secret key（指向 Anthropic / Bedrock / Azure / 本地兼容端点） |
| API token | `ANTHROPIC_AUTH_TOKEN` | Secret key |
| 其它 Claude Code 配置 | `~/.claude.json` | 通过 volume mount 挂入（`ConnectorBinding` 类型为 file） |

**好处：**
- Routines 不绑定任何模型供应商，用户随时换；
- 升级模型 / 切换供应商 = `kubectl edit secret`，不用改 Routine CR；
- 与 Claude Code 上游的配置约定保持一致，文档复用；
- Routines 的 schema 永远不需要追 LLM 行业的命名变化。

### API Surface - CRD 模型草案

> 字段由 `_bmad-output/architecture.md`（commit b0ec405）锁定。这里只重述**概念契约**；完整 v1alpha1 字段以架构文档为准。

**`Routine` (核心)**

```yaml
apiVersion: routines.a2d2.dev/v1alpha1
kind: Routine
metadata:
  name: nightly-issue-triage
  namespace: team-backend            # 所在 namespace = 环境边界
spec:
  prompt:
    inline: |
      Look at the latest 5 open issues in linear...
  repositoryRef:
    name: company-backend-repo
  connectorBindingRefs:
    - name: github-writer
    - name: linear-reader
    - name: anthropic-credentials    # 提供 ANTHROPIC_BASE_URL/MODEL/AUTH_TOKEN
  triggerRefs:
    - { kind: ScheduleTrigger, name: nightly-2am }
  maxDurationSeconds: 1800           # 单条 Message 的硬上限
  concurrencyPolicy: Forbid           # Forbid | Replace | Allow （作用于同一 Routine 的消息流）
  suspend: false
status:
  phase: Ready | Suspended | Terminating
  podReady: true
  currentMessageID: <opt>             # 当前正在处理的消息 ID
  lastMessageAt: <ts>
  gatewayRegistered: true             # Gateway 是否已为该 Routine 建好 queue dir
  conditions: [ ... ]
```

**Routine spec 字段总数：7 个**（prompt / repositoryRef / connectorBindingRefs / triggerRefs / maxDurationSeconds / concurrencyPolicy / suspend）。每一个都是 Routines Controller 在调度 / 执行 / 审计时**必须**读的字段。任何不属于这三件事的概念（模型、端点、token、Agent 实现选择）都不在 schema 里。Routine 对应运行时的**一个常驻 Pod**（StatefulSet replicas=1 + PVC），长生存 — 生命周期只由 CR 存在 / `spec.suspend` / 资源约束驱动，不因"一次触发"销毁。

**`ScheduleTrigger` / `WebhookTrigger` / `GitHubTrigger`**：独立 CRD，允许一个 trigger 被多个 Routine 复用，也允许一个 Routine 绑多个 trigger。`ScheduleTrigger` 的 `cron` 字段由 admission webhook 校验 **间隔不得低于 10 分钟**（默认策略，集群管理员可放宽）。

**`GitHubTrigger` — 表达式过滤（Playground 上下文）**

```yaml
apiVersion: routines.a2d2.dev/v1alpha1
kind: GitHubTrigger
metadata:
  name: external-pr-security-review
spec:
  installationRef: { name: a2d2-github-app }
  repositories:
    - owner: acme
      name: backend
  events:
    - pull_request
  # CEL 表达式，对下方的 event 上下文求值，true 时触发
  filter: |
    event.type == "pull_request" &&
    event.action in ["opened", "synchronize"] &&
    event.payload.pull_request.draft == false &&
    event.payload.pull_request.head.repo.fork == true
```

**Playground 上下文（CEL 求值的标准变量）：**

| 变量 | 含义 |
|---|---|
| `event.type` | 事件类型：`pull_request` / `push` / `issues` / ... |
| `event.action` | 子动作：`opened` / `closed` / `synchronize` / ... |
| `event.payload` | **原始 GitHub webhook payload**（完整 JSON，深度不限） |
| `event.deliveryID` | GitHub delivery id（幂等 key） |
| `event.repository` | 快捷访问 `payload.repository` |
| `event.sender` | 快捷访问 `payload.sender` |

用户可通过 `kubectl routines playground github-trigger <name> --payload ./sample.json` 命令在 CLI 里对一段真实 payload 即时求值表达式，看过滤结果（true/false + 匹配分支），以免发版后才发现表达式写错。

**`Message`（非 CRD，Gateway 内部对象）**：每次触发 Gateway 产生一条 Message，以 JSON 文件形式持久化到文件队列 `/data/queues/<routine-uid>/inbox/<ts>-<deliveryID>.json`。字段至少包含 `messageID` / `routineID` / `trigger.kind` / `trigger.name` / `payload.text`（freeform 字符串，承载 webhook body / GitHub event JSON / 手工输入，Claude Code 自行理解，保持与上游语义向前兼容）/ `enqueuedAt` / `deliveryID`（幂等 key）。Message **不进 etcd** —— 这是刻意设计，避免 K8s 控制面被短生命对象充满。

**Message 状态机**：`inbox → processing → done | failed`。迁移由 Agent Runtime 的 HTTP `lease` / `ack` / `nack` 触发，Gateway 负责文件原子 `rename()` 与 lease timeout（默认 `2 × maxDurationSeconds`）回滚。外部查询走 Gateway `GET /v1/history/<routine-uid>?since=`（CLI `routines history <routine>` 包装）；审计流走 Gateway `/data/events/<routine-uid>.jsonl`。

**`ConnectorBinding`**：一个 connector 名称 + 使用的 K8s Secret + scope（readonly/readwrite）+ 注入方式（env vars / file mount）。Routine 必须通过 `connectorBindingRefs` 显式引用（strict opt-in，见下方 Design Divergences）。Controller 在拉起 Routine Pod 时把 ConnectorBinding 渲染成 Pod env / volume mount — Claude Code 按约定读取（例如 `ANTHROPIC_BASE_URL` / `ANTHROPIC_AUTH_TOKEN`）。

### Authentication & Authorization

- **集群内认证**：完全依赖 K8s ServiceAccount 与 RBAC，不再实现自己的用户系统。
- **集群外入站（Webhook 端点）**：支持 (a) HMAC 签名（GitHub / Sentry 风格）；(b) Bearer token（MVP 直接使用 K8s Secret，**不提供 token 轮换 / 一次性显示 / revoke API**，管理员自行通过 `kubectl edit secret` rotate）；(c) 可选的 OIDC 前置（由用户自己的 ingress 控制）。
- **Routine → Connector**：通过 `connectorBindingRefs` 显式授权，不允许 Routine 自由读取 Secret。
- **Connector → 外部服务**：凭据存于 K8s Secret，Controller 以最小权限挂载到执行 Pod。
- **Agent 工具级权限**：MVP **不做**细粒度的工具权限控制（no permission-mode picker）。边界由 namespace 的 RBAC / NetworkPolicy / ResourceQuota 统一提供——这是 K8s 原生的权限层，已足够承担安全边界。Post-MVP 如有必要再引入。
- **LLM 凭据归属**：通过 ConnectorBinding 引用的 Secret 决定（哪个 Anthropic key / 哪个 Bedrock 账号 / 哪个本地端点）；Routines 本身不做身份建模。

### Design Divergences from Upstream Claude Routines

以下是**刻意**与上游不同的地方，在 docs 首页与迁移指南里要显式告知用户：

| 话题 | 上游 (Anthropic Claude Routines SaaS) | Routines (OSS K8s) | 理由 |
|---|---|---|---|
| 部署形态 | 闭源云端 | **K8s CRD + Operator，自托管** | 主权 / 合规 / 复用集群工具链 |
| Connector 授权 | **opt-out**：账户所有 connector 默认包含，按需移除 | **strict opt-in**：必须在 `connectorBindingRefs` 里显式引用 | K8s least-privilege；GitOps 可审计；避免默认权限膨胀 |
| 环境模型 | 独立 "Environment" 概念（网络 / env / setup） | 直接复用 K8s **namespace** | 复用管理员已会的工具链 |
| Branch push 默认 | `claude/*` 前缀 | **`routines/*` 前缀** | 项目独立命名 |
| Schedule 最小间隔 | 1 小时 | **10 分钟** | 自托管集群自行承担成本，无需上游的 SaaS 保护间隔 |
| API token 生命周期 | 一次性显示 / 轮换 / revoke UI | MVP 直接用 K8s Secret，手工 rotate | 自托管环境已有 Secret 管理，不重复造轮子 |
| GitHub filter | 表单字段（9 个固定字段） | **CEL 表达式 + Playground** | 表达式更灵活、可调试 |
| Agent 工具权限 | Permission tab（预配置） | MVP 不做，依赖 namespace RBAC/NetworkPolicy/Quota | K8s 原生边界已足够 |
| 会话续聊 | 是（session 可继续对话） | **是**（常驻 Pod + PVC + `claude --resume`；通过 `routines msg <routine>` 发新指令） | 核心对等能力，但映射为"常驻 AI 同事 + 消息流"而不是"每次起一个 Run" |
| 模型 / 端点 / token | 绑定 Anthropic 账号 | **任意（通过 Secret 注入 Claude Code 的标准 env vars）** | 不绑定供应商；本地推理、Bedrock、Azure 都行 |
| 所有权 | 绑定到个人 claude.ai 账号 | K8s 对象，谁能 RBAC 就归谁 | 与 K8s 生态一致 |

### Code Examples & Sample Library

官方 `routines-samples/` 仓库需至少包含以下示例（每个示例都是一个完整的 `Routine` YAML + README）：

- `hello-routine`：最小示例，一次性 cron，打印 "Hello from Routines"；
- `nightly-issue-triage`：Linear + Claude Code；
- `pr-security-review`：GitHub PR event → 安全审查 → 写 PR 评论；
- `doc-sync`：每周扫已合并 PR → 补文档 Draft PR；
- `alert-triage`：Alertmanager webhook → Slack 分类消息。

### Implementation Considerations

- **CRD 演进**：必须从 v1alpha1 开始，保留 conversion webhook 能力；v1.0 之前 alpha → beta → stable 路径清晰。
- **Controller 性能目标**：单 controller 实例支持至少 1,000 个 Routine、20 个并发 Run，不出现 reconcile backlog。
- **事件去重**：GitHub webhook 以 `X-GitHub-Delivery` header 作为幂等 key；通用 webhook 支持用户自定义 idempotency key。
- **日志 / 产物持久化**：MVP 使用 PVC；Growth 阶段接 S3 兼容对象存储。
- **Sandbox image 选择**：Helm value `agentImage` 暴露，默认 `ghcr.io/a2d2-dev/sandbox:latest`（含 Claude Code CLI）。用户可替换为自构建 image（例如 pin 到特定 Claude Code 版本、增加额外工具）。**Routines 不抽象 Agent 接口**，sandbox image 内的可执行入口由约定决定（默认 `claude` 命令）。
- **多租户隔离**：MVP 不做强多租户，但通过 Namespace + RBAC + Quota 可以支持"一个 namespace 一个团队"的软隔离。
- **升级兼容性**：CRD 字段只许添加不许删除；breaking change 只允许通过 new API version 发布。

---

## Project Scoping & Phased Development

### MVP Strategy & Philosophy

**MVP 策略：Problem-Solving MVP（解决核心问题的最小产品）**

Routines 的 MVP 哲学不是"做到和上游 Claude Routines 功能对等"，而是：

> **让一个已经对 Claude Routines 感兴趣、并且有合规/主权诉求的 K8s 用户，能在一周内跑起真实的 Routine 并替代 SaaS 版本在 80% 的场景下的作用。**

剩下 20% 的高级场景（Web UI、Prompt 版本管理、多集群编排等）留给 Growth Features。

**Resource Requirements（MVP 团队假设）：**
- 1 名 Go / K8s operator 开发经验充足的核心工程师（主力）
- 1 名熟悉 Claude Code 用法的工程师（兼职，负责 sandbox image 与示例）
- 1 名 Dev Rel / 文档 / 示例维护者（兼职）
- 可以是同一个人的 0.5 / 0.5 / 0.5，总量 ~1.5 人月即可交付 v0.1

### MVP Feature Set (Phase 1, v0.1 ~ v0.3)

**Core User Journeys Supported（见 User Journeys 章节）：**
- Journey 1（张浩的夜间 issue triage）— 完全覆盖
- Journey 2（刘静的告警分诊）— 基础覆盖（webhook + slack connector）
- Journey 3（王磊的运维）— 基础覆盖（RBAC / Quota / suspend）
- Journey 4（API 消费者）— webhook trigger 覆盖

**Must-Have Capabilities：**

- ✅ `Routine` CRD + Controller（Pod 物化 + PVC 生命周期 + Finalizer 保护）
- ✅ `ScheduleTrigger` / `WebhookTrigger` / `GitHubTrigger` 三种触发器 CRD
- ✅ **Gateway**：集群共享 Deployment（MVP `replicas: 1`）+ RWO PVC + HTTP API + 文件队列 + 审计流
- ✅ `ConnectorBinding` CRD + Git connector（GitHub 读写 + 开 PR）+ Anthropic 凭据注入
- ✅ Sandbox image (内置 `routines-agent` + Claude Code) 由 Helm value 暴露
- ✅ Helm chart（含 Gateway 部署）+ kind quickstart
- ✅ `maxDurationSeconds` 单消息硬上限
- ✅ concurrencyPolicy（Forbid / Replace / Allow，作用于消息流）
- ✅ 基础幂等（GitHub delivery id 作为 Gateway `deliveryID`、用户自定义 idempotency key）
- ✅ kubectl-friendly printer columns + status conditions（`Routine.status.{phase, podReady, currentMessageID, lastMessageAt}`）
- ✅ `spec.suspend` 可热暂停（StatefulSet → replicas=0）
- ✅ **Message 级 session 续聊**：`routines msg <routine> "<text>"` → Gateway enqueue → Agent Runtime `claude --resume`
- ✅ Samples 仓库至少 4 个可运行示例
- ✅ Docs：install / first routine / security model / migration guide（from upstream Claude Routines）

### Post-MVP Features

**Phase 2 (Growth, v0.4 ~ v0.9)：**
- 更多 Connector：Linear / Slack / Jira / Sentry / Notion / Google Drive
- Prompt 版本管理（Routine spec 引用 ConfigMap / OCI artifact）
- 只读 Web Dashboard（可选组件）
- Run 产物对象存储支持（S3 兼容）
- 通用 Webhook signature verification 框架
- Event source 扩展：GitLab / Bitbucket / Cloud Events
- Routine 模板市场（`routines-contrib/templates/`）
- Token / 美元预算 sidecar（可选挂载，承担计费可观测性）
- **可选：Agent 抽象引入**（仅在出现明确需求时）

**Phase 3 (Expansion, v1.0+)：**
- Agent Mesh：Routine 之间通过事件 DAG 编排
- 多集群分发（control plane / worker cluster）
- Policy as Code：OPA / Gatekeeper 集成
- Enterprise：审计日志外发 SIEM、SAML/OIDC、多租户 quota billing
- Routine as a Product：Platform 团队向内发布 self-service 表单
- **Hosted Trigger Hub**（官方云端商业化入口）

### Risk Mitigation Strategy

**Technical Risks：**
- *Risk:* Controller 在大量并发下 reconcile 性能退化。
  *Mitigation:* 从第一天就引入 controller-runtime 的 workqueue best practice；write scale test harness 作为 CI 一环。
- *Risk:* Claude Code CLI 接口变化破坏 sandbox image 行为。
  *Mitigation:* sandbox image 由用户控制版本（Helm value pin digest）；core controller 不依赖 Claude Code 的具体输出格式，只看退出码 + PVC 中的 outputs 文件。
- *Risk:* CRD 字段早期频繁 breaking change。
  *Mitigation:* 严格 v1alpha1 → v1beta1 → v1 演进；提供 conversion webhook；文档明确"早期 alpha 不保证向后兼容"。

**Market / Adoption Risks：**
- *Risk:* "自己部署 K8s 太麻烦" 让个人开发者望而却步。
  *Mitigation:* 提供 kind + 单节点 k3s 的 15 分钟 quickstart；把 docs 首页 demo 设计成"5 分钟可运行"。
- *Risk:* Anthropic 开放 Claude Routines 自托管 / 提供官方 K8s 版本。
  *Mitigation:* 我们的差异化不是"复刻 Claude Routines"，而是"K8s 原生 + GitOps 心智 + 任意 LLM 端点"；即便上游开放自托管，K8s 优先的工具链选型仍有独立价值。

**Resource / Community Risks：**
- *Risk:* 核心维护者精力有限，PR review 堆积导致社区流失。
  *Mitigation:* 早期明确 `routines-core` vs `routines-contrib` 分仓治理；社区 connector 允许快速 merge 到 contrib；core 仓保守收紧。
- *Risk:* 开源项目常见的 "初期热度 → 后期沉寂" 曲线。
  *Mitigation:* 从第一天就有 roadmap、monthly release cadence、且 dogfood 自己的 issue triage Routine 维护项目本身。

### Scope Decision Summary

| 能力 | MVP (v0.1) | Growth (v0.x) | Vision (v1+) |
|---|:---:|:---:|:---:|
| 定时触发（≥ 10min cron） | ✅ | | |
| Webhook 触发（HMAC + Bearer） | ✅ | | |
| GitHub 事件触发 + CEL filter | ✅ | | |
| filter Playground (CLI) | ✅ | | |
| GitLab/Bitbucket 事件 | | ✅ | |
| 内置 Claude Code (via sandbox image) | ✅ | | |
| Agent 抽象（AgentProfile-like CRD） | ❌ | 按需引入 | |
| **Gateway 消息中枢（`replicas: 1` + RWO PVC + 文件队列）** | ✅ | | |
| **Gateway HA（RWX PVC 或外部 MQ 替换文件队列）** | ❌ | ✅ | ✅ |
| **Message 级 session 续聊（`routines msg`）** | ✅ | | |
| Git Connector (R/W) | ✅ | | |
| Linear / Slack / Sentry Connectors | 可选，不强制 | ✅ | |
| Helm 安装 | ✅ | | |
| Web Dashboard | ❌ | ✅（只读） | ✅（读写） |
| `maxDurationSeconds` 硬上限 | ✅ | | |
| Token / USD 预算 sidecar | ❌ | ✅（可选） | |
| API token 生命周期管理 | ❌（手工 rotate） | ✅ | |
| Agent 工具级权限 | ❌（仅 namespace RBAC） | ❌ | ✅ |
| 多集群 / Control Plane | ❌ | ❌ | ✅ |
| OPA / Policy 集成 | ❌ | ❌ | ✅ |
| **Hosted Trigger Hub（官方云端）** | ❌ | ❌ | ✅ |

---

## Functional Requirements

> 这是 Routines 的 **Capability Contract**。UX / Architecture / Epic 拆分都只能基于此处列出的能力展开；任何未在此处出现的能力都不会存在于最终产品中。MVP 覆盖 FR1–FR35（含 FR8a/8b、FR13a/13b）；FR36+ 为 Post-MVP / Growth。

### Routine Lifecycle Management

- **FR1**：开发者可以通过 `kubectl apply` 一个 `Routine` CR 来声明一个 AI 自动化任务，包括 prompt、repo、triggers、connector bindings、maxDurationSeconds 和 concurrency 策略。
- **FR2**：开发者可以通过 `kubectl edit` 或 patch 更新 Routine 的 prompt、trigger、maxDurationSeconds 等字段，更新对正在进行中的 Run 不产生追溯性影响（下一次触发起生效）。
- **FR3**：开发者可以通过设置 `spec.suspend: true` 让一个 Routine 进入暂停状态；Controller 把对应 StatefulSet 缩容到 `replicas=0`（Pod 停、PVC 保留），Gateway 不再为该 Routine 入队新 Message（已 `inbox/` 中的消息保留，`spec.suspend: false` 后 Pod 拉起即可续处理）。
- **FR4**：开发者可以通过 `kubectl delete routine <name>` 删除一个 Routine；删除会级联清理其 Gateway queue 目录注册、trigger 的活动订阅（webhook ingress 路由、GitHub subscription 缓存）；PVC 默认由 Finalizer 保护，需显式声明 `--force-keep-pvc=false` 才 GC（会话历史是 "AI 同事几个月的记忆"，避免误删）。已入 Gateway `done/` / `failed/` 的消息归档不随 Routine 删除丢失。
- **FR5**：平台管理员可以查询某个 namespace 或集群中所有 Routine 的列表及其状态、下次触发时间、最近一次 Message 结果（通过 kubectl 的 printer columns 展示 `phase` / `podReady` / `currentMessageID` / `lastMessageAt`），以及通过 `routines history <routine>` 获取详细消息流水。

### Trigger Management

- **FR6**：开发者可以声明一个 `ScheduleTrigger`，使用标准 cron 语法与时区字段，定义周期性触发。默认策略下 admission webhook 拒绝最小间隔小于 **10 分钟** 的 cron 表达式；集群管理员可通过 Helm values 放宽或收紧此阈值。
- **FR7**：开发者可以声明一个 `WebhookTrigger`，由 Routines 自动在 ingress 上开出一个 stable HTTP 端点，支持 HMAC 签名与 Bearer token 校验。MVP 的 bearer token 直接存放于用户创建的 K8s Secret，不提供 token 轮换 / 一次性显示 / revoke 流程 — 管理员通过标准的 `kubectl edit secret` 自行 rotate。
- **FR8**：开发者可以声明一个 `GitHubTrigger`，订阅一个或多个仓库的事件（pull_request、push、issues、release、workflow_run 等）。MVP 至少支持 pull_request / push / issues / issue_comment / release 五类事件。
- **FR8a**：`GitHubTrigger` 支持通过 **CEL 表达式** 对 `filter` 字段求值，实现事件过滤。表达式在 **Playground 上下文** 中求值（见 CRD 草案章节），可用变量包括 `event.type` / `event.action` / `event.payload` / `event.deliveryID` / `event.repository` / `event.sender`。表达式为空或求值为 `true` 时 Gateway 入队 Message。
- **FR8b**：Routines 提供 `kubectl routines playground github-trigger <name> --payload <file.json>` 命令，让开发者在不真正触发的前提下，对一段真实的 GitHub payload 即时求值 filter 表达式，返回 true/false 以及表达式内每个子条件的求值结果。
- **FR9**：单个 Routine 可以绑定多个触发器（例如同时有 `ScheduleTrigger` 和 `GitHubTrigger`）；任一触发器触发都会经 Gateway 入队一条 Message 到该 Routine 的 `inbox/`。
- **FR10**：开发者可以通过 `routines run now <routine>` 或等价 Gateway HTTP `POST /v1/enqueue` 手工入队一条 Message 立即派发给 Routine，用于测试或紧急任务；`routines msg <routine> "<text>"` 是其 freeform payload 变体（见 FR13a）。
- **FR11**：Routines 保证对每一个入站事件的幂等处理 — 相同的 `deliveryID`（GitHub delivery id、webhook HMAC nonce 或用户自定义 idempotency key）在配置窗口内只会被 Gateway 入队为一条 Message（重复入队会被 Gateway 拒绝或去重）。

### Gateway — Message Delivery

- **FR11a**：Routines 部署一个**集群共享 Gateway**（Deployment + RWO PVC）作为三种触发源（cron / webhook / GitHub event）的唯一入口，以及 Routine Pod 拉取消息的唯一出口。Gateway 暴露 HTTP API：`POST /v1/enqueue`、`POST /webhooks/<trigger-name>`、`POST /webhooks/github/<installation>`、`GET /v1/lease/<routine-uid>?wait=<s>`（长轮询）、`POST /v1/ack/<routine-uid>/<messageID>`、`POST /v1/nack/<routine-uid>/<messageID>`、`POST /v1/heartbeat/<routine-uid>`、`GET /v1/history/<routine-uid>?since=`。**MVP Gateway `replicas: 1`**（RWO PVC 限制使 2 副本 + leader election 无实际收益，follower 无法读 PVC）；HA 方案（RWX PVC 或外部 MQ）延后到 Phase 2。
- **FR11b**：Gateway 的文件队列布局（PVC `/data/`）遵循"一切是文件"原则：`queues/<routine-uid>/{inbox,processing,done,failed}/<ts>-<deliveryID>.json`（每条 Message 一个 JSON 文件，状态迁移 = POSIX `rename()`）+ `events/<routine-uid>.jsonl`（append-only 审计流）+ `sessions/<routine-uid>/meta.json`（当前 Claude Code session-id 等元数据）。**无外部依赖**（不引入 NATS / Redis / PostgreSQL）。

### Routine Pod Lifecycle

- **FR12**：Controller 在 reconcile `Routine` CR 时为其物化一个 **StatefulSet (replicas=1) + 专属 PVC** 常驻 Pod（"长期在岗的 AI 同事"）。Pod 镜像为 sandbox image（Helm value `agentImage`，默认 `ghcr.io/a2d2-dev/sandbox:latest`），ENTRYPOINT 是 `routines-agent` 二进制（即 Agent Runtime）。**Pod 长生存** — 生命周期只由 `Routine` CR 的存在 / `spec.suspend` / 资源约束 / Pod 健康状态驱动，不因"一次触发"销毁。Controller 完成创建后通知 Gateway 注册对应的 queue 目录（`gatewayRegistered: true`）。
- **FR13**：Routine 的 PVC（`/work/`）承载四类状态：(a) repo 工作区（`repo/`）；(b) Claude Code session 目录（`sessions/<session-uuid>.jsonl` 与 `session-id` 文件，`--resume` 读取）；(c) 每条消息的运行日志（`logs/<messageID>/{stdout,stderr}.log`）；(d) 可选的产物输出（`outputs/`）。Routine 的 `/work/routine/prompt.md` 由 Controller 按 `Routine.spec.prompt` 渲染写入；`/work/routine/config.json` 记录 connector 映射。

### Message Lifecycle & Session Continuity

- **FR13a · Message 级 session 续聊（取代原"Run 可恢复"）**：用户可以通过 `routines msg <routine> "<text>"`（或等价 Gateway `POST /v1/enqueue`）给一个现有 Routine 发送任意 freeform 指令，作为一条新 Message 入队 `inbox/`。Agent Runtime 在下一次心跳里 lease 到该消息后，以 `claude --resume <session-id>` 拉起新 `claude` 子进程；Claude Code 读取同一 PVC 上既有 `/work/sessions/<session-uuid>.jsonl` 的 session state 并继续对话 —— 相当于给常驻 AI 同事补一条新指令。**Continue 不是"重启一个旧 Run"，而是"给一个在岗员工派一个新任务"**。
- **FR13b · Session 与消息保留**：`Routine.spec.sessionRetention` 字段配置保留策略：`keepAll` / `ttl:<duration>`（默认 `ttl: 90d`）；对常驻 Routine 不适用"keepLast:N"（不再是 N 个独立 Run 对象）。Gateway 的 sweep goroutine 定期清理 `done/` 与 `failed/` 目录中超过 TTL 的 Message 文件，以及 `events/<routine-uid>.jsonl` 的 rotate；session state 文件在 Routine 删除时才清理（见 FR4）。`sessionRetention.rotateOnContextOverflow: true` 是 Phase 2 项（Claude Code context 溢出时自动开新 session）。
- **FR14 · Message 状态机**：每条 Message 的状态机为 `inbox → processing → done | failed`，迁移由 Agent Runtime 的 `lease` / `ack` / `nack` HTTP 调用触发；Gateway 负责文件原子 `rename()` 与 lease timeout 回滚（默认 `2 × maxDurationSeconds`：超时未 ack 则 `processing → inbox` 重投，或超过重试次数后 `processing → failed`）。每次迁移同时 append 一条记录到 `events/<routine-uid>.jsonl`（审计流）。Message `payload.text` 承载触发载荷的 freeform 字符串（webhook body / GitHub event JSON 序列化 / 手工输入），**Routines 不对 payload 做结构化解析**，Claude Code 自行理解；这保持与上游语义向前兼容。
- **FR15 · 取消当前 in-flight Message**：平台管理员和 Routine 所有者可以通过 `routines cancel <routine>` 取消**当前正在处理的 Message**（不是整个 Routine）—— Agent Runtime 收到 cancel 信号后对 `claude` 子进程 SIGTERM → grace → SIGKILL，随后向 Gateway `nack` 并带 `reason=cancelled`，Gateway 把 Message 迁到 `failed/`。Routine Pod 本身继续在岗，下一次心跳里拉取下一条消息。**取消整个 Routine 用 `kubectl delete routine`**（PVC 由 Finalizer 保护）。
- **FR16 · maxDurationSeconds**：Routines 为**每条 Message** 强制执行 `Routine.spec.maxDurationSeconds` 硬上限，超时后 Agent Runtime SIGTERM/SIGKILL `claude` 子进程、`nack` 给 Gateway 并带 `reason=timeout`，Gateway 把 Message 迁到 `failed/`；Routine Pod 继续存活处理下一条消息。FR13a 的 "`routines msg`" 消息与常规触发消息**受同一个 `maxDurationSeconds` 约束**（每条消息独立时间预算）。
- **FR17 · 并发策略**：Routines 根据 `Routine.spec.concurrencyPolicy` 控制**同一 Routine 的消息并发**：`Forbid`（默认，单 Pod 里消息严格 FIFO 串行，与 `replicas=1` 天然一致）；`Replace`（新消息入队时 Gateway 通知 Agent Runtime 取消当前 in-flight message，类似 FR15 的 cancel 语义）；`Allow`（保留字段供 Phase 2 多 session 支持，MVP 等价 `Forbid`）。同一 Routine 内的消息默认共享同一 Claude Code session（`--resume`），`concurrencyPolicy` 不改变这个 session 模型。

### Sandbox Runtime (Claude Code)

- **FR18**：Routines 在执行 Pod 内使用一个 sandbox image 运行 Claude Code。Sandbox image 通过 Helm value `agentImage` 配置（默认 `ghcr.io/a2d2-dev/sandbox:latest`），用户可替换为自构建版本。
- **FR19**：Controller 在拉起 Pod 时按以下约定准备工作环境：
  - 把 repo checkout 到 PVC 中的工作目录，作为 Pod 的 `WORKDIR`；
  - 把 Routine 的 prompt 写入 PVC 中的固定文件路径；
  - 把 ConnectorBinding 引用的 Secret 按其声明（env vars 或 file mount）注入 Pod —— 这是 Claude Code 读取 `ANTHROPIC_BASE_URL` / `ANTHROPIC_AUTH_TOKEN` / `ANTHROPIC_MODEL` 等标准 env vars 的来源；
  - 把 Claude Code 的 session 目录指向 PVC 上的持久化路径，使 `claude --resume <session-id>` 可以无损延续（见 FR13a）；
  - 入口命令默认调用 `routines-agent`（Agent Runtime），由 sandbox image 自身保证 `routines-agent` + `claude` 均可执行（用户自构建 image 时需保留这两个约定）。
- **FR20 · 退出码语义（Agent Runtime 对 Claude Code 子进程的契约）**：`0` = Succeeded → Agent Runtime `ack` 并保留 session-id；非零 = Failed → `nack`（默认 `retryable=false`，Agent Runtime 可按错误类型决定）；`75` (EX_TEMPFAIL) = **Resumable / "session parked"** → `ack` 该消息但**显式保留 session-id + events.jsonl 追加 `sessionParked` 事件**，暗示 "任务未完成，下一条消息继续在同一 session 里续聊"（区别于 `exit 0` 的"自然完成"）。下一条消息到达时 Agent Runtime 以 `claude --resume` 衔接。

### Connector & Secret Management

- **FR21**：平台管理员可以声明一个 `ConnectorBinding` CR，把 K8s Secret 与一个具名 connector（例如 `github`、`linear`、`slack`、`anthropic`）绑定，并指定 scope（readonly / readwrite）和注入方式（env / file）。
- **FR22**：Routine 通过 `connectorBindingRefs` 显式引用 ConnectorBinding（**strict opt-in**，与上游 opt-out 模型刻意不同）；没有引用的 Connector 在执行 Pod 内不可访问。
- **FR23**：Routines 保证 Routine 作者（非平台管理员）不能直接读取 Connector 所引用的 K8s Secret 明文（只能通过 Binding 引用）。
- **FR24**：MVP 至少内置一个 Git / GitHub Connector，支持 clone、commit、push、开 PR、评论 PR、评论 Issue 的能力。**MVP 默认 push 不限制分支前缀**（移除安全限制以简化 MVP）；推荐示例和文档中引导用户使用 `routines/*` 前缀作为约定俗成的 routine 工作分支命名。

### Audit, Observability & Diagnostics

- **FR25**：Gateway 的 `events/<routine-uid>.jsonl` 对每条 Message 的每一次状态迁移 append 一条审计记录，字段至少含：`messageID`、`routineID`、`trigger.kind/name`、`promptHash`、`sandboxImageDigest`、`startedAt` / `endedAt`、`exitCode`、`reason`（成功 / 超时 / 取消 / 失败分类）、`tokenUsage`（由 Agent Runtime 从 CC stdout 解析）。
- **FR26**：每条 Message 的外部工件（PR URL、commit SHA、评论 URL、Slack 消息 ts 等）写入 Routine PVC 的 `/work/outputs/<messageID>/` 并同步到 Gateway `events.jsonl` 的对应 `ack` 事件的 `outputs` 字段，便于 `routines history` 检索。
- **FR27**：`kubectl describe routine <name>` 能直接展示 Pod 健康、`currentMessageID`、`lastMessageAt` 和最近一次失败的原因摘要；`routines history <routine>` + `routines logs <routine> <messageID>` 展示完整流水（含 Claude Code 退出码、stderr 摘要、超时 / 取消提示等）。
- **FR28**：Message 执行日志持久化到可配置后端（MVP：Routine PVC `/work/logs/<messageID>/` + Gateway `events.jsonl`；Growth：S3 兼容归档），保留策略可配置（见 FR13b `sessionRetention`）。
- **FR29**：平台管理员可以通过 `routines history -A --since=<duration>` 或 Prometheus metrics（见 NFR-I3）查询某个时间窗口内所有 Message 的汇总（成功率、失败原因分布、运行时长分布）。

### Security & Blast Radius

- **FR30**：Routines 的默认 install 以最小权限 RBAC 运行，core controller / Gateway 均不持有集群级别 `cluster-admin` 权限；Controller 只能管理自身 CRD + 指定 namespace 的 StatefulSet/PVC/Pod；Gateway 只读对应 Trigger CR、读 Routine UID 映射。
- **FR31**：Routine Pod 与 Gateway Pod 默认禁止 host network、host path 挂载和 privileged 模式；默认以非 root UID 运行。
- **FR32**：Routines 拒绝 Routine 访问任何未通过 ConnectorBinding 声明的 K8s Secret；尝试直接引用外部 Secret 的 Routine 在 admission 阶段被拒绝。Gateway 同样拒绝来自 Routine Pod 之外的 `lease` / `ack` / `nack` 调用（通过 Pod identity / mTLS 或 token 验证）。

### Installation & Dev Experience

- **FR33**：Routines 可通过官方 Helm chart 一次命令安装，Chart 自动注册 CRD、创建 namespace、部署 Controller + **Gateway Deployment (MVP `replicas: 1`) + Gateway PVC** 以及 webhook ingress；Chart 暴露 `agentImage` value 让用户覆盖默认 sandbox image，暴露 `gateway.replicas`（Phase 2 解锁）和 `gateway.storage.size` / `storageClassName` 等 values。
- **FR34**：Routines 提供 `kind` / `k3d` 本地 quickstart 脚本，让新用户在本机无需真实云集群即可体验第一个示例 Routine（自动创建 Gateway PVC / Routine PVC 所需的 local-path storage class）。
- **FR35**：Routines 提供一个可选 CLI `kubectl-routines`（作为 kubectl plugin），支持：`list`（列所有 Routine）、`logs <routine> [<messageID>]`（查单条消息日志）、`run now <routine>`（手工入队空 payload）、`msg <routine> "<text>"`（freeform payload，FR13a）、`history <routine> [--since <duration>]`（Gateway 消息流水）、`cancel <routine>`（取消当前 in-flight message，FR15）、`describe <routine>`、`playground github-trigger <name> --payload <file>`（FR8b）等便捷命令。**刻意不提供 `continue <run>`**：Routine 是常驻 Pod，用 `msg <routine>` 派新指令即可续聊；没有"Run"这个中间概念。

### Growth / Post-MVP（非 MVP 必交付）

- **FR36**：开发者可以通过 Web Dashboard 只读查看 Routine 列表、每个 Routine 的 Message 流水、日志和产物。
- **FR37**：Routines 支持 GitLab 与 Bitbucket 作为事件源与 Git Connector。
- **FR38**：平台管理员可以通过 `RoutineTemplate` CR 分发可参数化的 Routine 模板，开发者只需填少量字段即可实例化。
- **FR39**：Routines 支持 OPA / Gatekeeper 策略，集群管理员可以对 Routine 施加策略约束（最大 maxDurationSeconds、允许的 Connector、允许的 sandbox image registry 等）。
- **FR40**：Routines 支持 Event Mesh：一个 Routine 的消息成功完成可以发出集群内事件（或由 Gateway 反向 enqueue），另一个 Routine 的 trigger 可以订阅该事件，形成 DAG 编排。
- **FR41**：可选的 token / 美元预算 sidecar，挂载到 Routine Pod 中，承担 LLM 计费可观测性与超额熔断。
- **FR42 · Gateway HA**：Gateway 升级为支持多副本（`replicas: 2+`）+ leader election，底座从 RWO PVC 切换到 **RWX PVC** 或外部 MQ（NATS JetStream / Redis Streams），消除当前 MVP 的单点风险。MVP 的文件队列与 HTTP API 契约保持不变，升级对 Routine Pod 透明。

---

## Non-Functional Requirements

> 本节只列出对 Routines 实际产生约束的质量属性。无关类目（例如 UI 可访问性、传统 B2C 相关 SEO）已省略。

### Performance

- **NFR-P1 · 触发延迟**：从触发事件到达到 Gateway `inbox/` 落盘 + Routine Pod `lease` 到消息 + `claude` 进程开始处理 prompt，MVP 目标中位延迟 ≤ 30 秒 / P99 ≤ 90 秒；v1.0 目标中位 ≤ 10 秒 / P99 ≤ 60 秒。（与 Measurable Outcomes 表一致。）
- **NFR-P2 · Controller Reconcile**：单 Routine Controller 实例在负载 1,000 个 Routine（即 1,000 个常驻 Pod）稳态下，reconcile 队列长度 < 50、P99 reconcile 时间 < 500ms。
- **NFR-P3 · Gateway 入站吞吐**：Gateway 单副本（MVP 形态）支持 ≥ 100 req/s 的验签 + 入队吞吐，响应 P99 < 200ms（不计 Message 执行）。
- **NFR-P4 · Routine Pod 启动成本（首次 / 滚动升级）**：Pod 启动到 Agent Runtime 开始 `lease` 的额外开销（PVC mount + init）P50 ≤ 10 秒、P99 ≤ 30 秒。常驻 Pod 稳态运行时此指标不重复发生。
- **NFR-P5 · Message 续聊启动成本**：从 Gateway 入队新消息到 Agent Runtime `lease` + `claude --resume` 进程启动并 load session state 的额外开销 P50 ≤ 15 秒、P99 ≤ 45 秒。

### Reliability

- **NFR-R1 · Controller / Gateway HA**：Controller 支持 leader election + 多副本热备，故障切换 RTO ≤ 30 秒。**Gateway MVP 为 `replicas: 1`**（RWO PVC 限制下 2 副本 + leader election 无实际收益，follower 无法读 PVC）；Gateway 崩溃时 Pod-level restart（K8s liveness）是 MVP 的恢复手段，RTO 目标 ≤ 60 秒；真正的多副本 HA（RWX PVC 或外部 MQ）由 FR42 在 Phase 2 解决。
- **NFR-R2 · 事件不丢**：Gateway 在 Routine Pod 不可达（崩溃、滚动升级、`suspend`）时仍能正常入队 Message 到 `inbox/`；Gateway 本身崩溃 → Pod 重启期间入站事件由 K8s Service 的 retry / 上游系统的 webhook 重投承载（至少 15 分钟缓冲期，借助入站客户端重试）。Routine Pod 崩溃时，`processing/` 中的消息在 lease timeout 后由 Gateway 自动 `processing → inbox` 或 `processing → failed`（见 FR14）。
- **NFR-R3 · 幂等保证**：同一触发事件（以 `deliveryID` 为主键）在 24 小时窗口内最多被 Gateway 入队为 1 条 Message，重启、重放场景下保持该语义（幂等检查在 `inbox/` + `processing/` + `done/` 三处扫已有 `deliveryID`，或维护一个短生命 in-memory LRU）。
- **NFR-R4 · 升级兼容**：v0.x 的 Helm upgrade 不得破坏已经在集群中运行的 `Routine` CR；CRD schema 变更必须走 conversion webhook 或 new API version。Gateway 升级必须向前兼容既有 Message 文件格式（JSON schema 可加字段，不可删字段）。
- **NFR-R5 · Message 隔离**：单条 Message 的崩溃、死锁或超时不得影响同一 Routine 的下一条 Message（Agent Runtime 进程重启 / `claude` 子进程重启）或其他 Routine；Routine 之间的 PVC / queue 目录严格隔离。

### Security

- **NFR-S1 · 最小权限**：Routines controller 所需的 ClusterRole 只包含对自身 3 个 CRD（`Routine`、`<Schedule|Webhook|GitHub>Trigger`、`ConnectorBinding`）的读写和对 StatefulSets/Pods/PVCs/Secrets（限定 namespace）的管理权限；Gateway 只需对其自身 PVC 和 namespace-scoped Services/Endpoints 的权限。严禁 `*` 级通配 RBAC。
- **NFR-S2 · Secret 隔离**：Routine 执行 Pod 只能读取通过 `ConnectorBinding` 显式引用的 Secret；任何尝试直接 mount 集群其他 Secret 的 Routine 会在 admission webhook 阶段被拒绝。
- **NFR-S3 · 签名校验**：所有入站 Webhook 默认要求签名校验（HMAC 或 Bearer token），未通过校验的请求直接 401，不进入后续处理。
- **NFR-S4 · Pod 运行时加固**：执行 Pod 默认运行在 `runAsNonRoot: true`、`readOnlyRootFilesystem: true`（PVC 工作区除外）、`allowPrivilegeEscalation: false`、禁用 host network / host path 的安全上下文中。
- **NFR-S5 · Supply Chain**：官方发布的 controller / webhook image 必须带 SBOM 和 cosign 签名；Helm chart 也必须签名；默认 sandbox image 同样签名并发布 SBOM。
- **NFR-S6 · Prompt Injection 缓解**：默认 Connector 仅授予必要 scope（例如 GitHub token 不授予 repo admin、org admin 权限）；文档与 sample 提供"安全默认"的 ConnectorBinding 模板。
- **NFR-S7 · 审计完整性**：Gateway `events.jsonl`（Message 生命周期）、Routine PVC `logs/<messageID>/`（执行细节）以及 Message 产生的外部副作用（PR、commit、评论）在 Routine 被删除后仍可通过外部日志 / 对象存储归档查到；保留策略可配置但默认 ≥ 90 天。

### Scalability

- **NFR-SC1 · 集群内规模**：单集群 Routines 部署支持 ≥ 1,000 个 `Routine`（等同于 ≥ 1,000 个常驻 StatefulSet Pod）、≥ 500 并发在途 Message（v1.0 目标；MVP：≥ 200 个 Routine / ≥ 20 并发 Message）。Routine 数量受限于集群整体 Pod/PVC 配额，不受 Gateway 单副本约束。
- **NFR-SC2 · 水平扩展**：Controller 通过 leader election 做 active-standby（不做 sharding，直到有明确 scale pain point）；Gateway MVP 单副本，Phase 2 通过 RWX PVC 或外部 MQ 支持 ≥ 2 副本（见 FR42）；Agent Runtime 以"一个 Routine = 一个 Pod"方式天然横向扩展。
- **NFR-SC3 · 资源隔离**：每个 Routine Pod（Agent Runtime + Claude Code 子进程）必须声明 requests / limits；Routines 对默认值提供安全下限（CPU/memory），避免无声明 Pod 踩踏共享节点。

### Integration

- **NFR-I1 · K8s 版本兼容**：MVP 支持上游 K8s 1.27+、k3s / k3d、EKS、GKE、AKS；每个 release 声明 test matrix。
- **NFR-I2 · Sandbox image 兼容**：默认 sandbox image 跟随 Claude Code 上游版本节奏发布；用户使用 image digest pinning 锁定版本以防上游变更。Agent Runtime（`routines-agent`）与 Claude Code 子进程通过退出码 + PVC `outputs/` 文件解耦，Routines 自身不解析 Claude Code 的 stdout 格式。
- **NFR-I3 · 观测接入**：Routines 暴露 Prometheus metrics（controller reconcile 延迟、Gateway 队列深度、Message 成功率、Message 运行时长分布、session 续聊命中率等），并且 Routine 状态变更（`Ready`、`Paused`、`Degraded`）与 Message 生命周期事件发出 K8s Events 便于既有监控接入。
- **NFR-I4 · 日志接入**：Routine Pod（Agent Runtime + Claude Code 子进程）的 stdout/stderr 走标准 K8s 日志机制；Gateway 的 HTTP 访问日志和 `events.jsonl` 同样走标准 stdout/stderr + PVC，便于用户现有 Loki / EFK / CloudWatch 管道抓取。

### Maintainability (OSS-specific)

- **NFR-M1 · 代码质量**：Go 代码通过 `go vet`、`golangci-lint`、`staticcheck`；核心 controller 单测覆盖率 ≥ 70%。
- **NFR-M2 · E2E 测试**：每个 release 必须通过基于 envtest + kind 的端到端测试，覆盖"创建 Routine → Pod Ready → Trigger 发射 → Gateway enqueue → Pod lease → Message 完成（ack 或 nack）→ `routines history` 可见"的主路径，以及"`routines msg` 续聊"支线。
- **NFR-M3 · 文档同步**：文档站（Docusaurus / mkdocs 等）的内容由 repo 中 markdown 生成，任何 API / CRD 字段变更必须同一 PR 更新文档；CI 做 "docs drift" 检查。
- **NFR-M4 · Release cadence**：alpha 阶段至少每月一次 minor release；v0.1 之后公告 breaking change 至少领先 2 个版本。
