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
revision: 3
revisionNotes: 'Major simplification (revision 3): removed AgentProfile CRD, Routine.spec.model, budget.maxTokens/USD, "Agent runtime neutral" positioning, Journey 4, T4, FR18-22. Repositioned as "Self-hosted K8s-native Claude Routines clone", leveraging the heat of K8s and Anthropic Claude Routines. Routines schema only models scheduling/execution/audit — Claude Code is a sandbox image (configured via Helm value), model/endpoint/token are mounted via Secret as Claude Code'+"'"+'s own env vars. Routine spec reduced to 7 fields.'
---

# Product Requirements Document - Routines

**Author:** Neov
**Date:** 2026-04-16

---

## Executive Summary

### 产品愿景

**Routines 是 [Claude Routines](https://code.claude.com/docs/en/routines) 的 Kubernetes 自托管开源版。**

上游 Anthropic Claude Routines 是 SaaS：必须绑 claude.ai 账号、跑在 Anthropic 基础设施上、按它的环境模型来组织。Routines 把同样的能力（cron / webhook / GitHub 事件触发 → Claude Code 自动跑一段任务 → 提 PR / 写评论 / 发 Slack）做成一组**标准 K8s CRD + Operator**，让你在自己的集群里跑，复用集群已有的 RBAC、NetworkPolicy、Quota、GitOps、Secret 管理。

你定义一次 Prompt、一个代码仓库、几个连接器、一组触发器 —— 然后 Routines Controller 在该触发的时候在你的集群里拉起一个 Pod，跑 Claude Code，把结果（commit / PR / Slack 消息）作为一个可审计的 RoutineRun 对象写回集群。

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
Routines 不是一个容器化的后端服务，而是一组**自定义资源 (CRD) + Operator**。`Routine`、`RoutineTrigger`、`RoutineRun`、`ConnectorBinding` 都是一等 K8s 对象，可以 `kubectl apply`、被 GitOps 管理、被 RBAC 控制、被 ArgoCD 同步。工程师熟悉的所有 K8s 工具链立即可用。

**3. 三种触发方式即插即用**
定时（cron）、Webhook、GitHub 事件三条触发路径原生集成，在同一个 `Routine` 上可叠加使用。不需要用户自己搭 Jenkins / GitHub Actions / n8n 的胶水层。

**4. 可恢复的 Run：会话延续是一等公民**
每个 `RoutineRun` 使用 **StatefulSet + PVC** 运行，工作目录、git worktree 与 Claude Code 会话状态持久保存。用户可以对任意一次已完成 Run 发起 `continue` 命令，Controller 在同一 PVC 上拉起新 Pod 继续与 Claude Code 对话——相当于 K8s 里的可重入 session。

**5. 可观测、可审计、可重放**
每一次 `RoutineRun` 都是一个可被 `kubectl get` / `kubectl describe` 的对象，包含完整的触发来源、Prompt 版本、Claude Code 输出、产生的 Git commits / PR 链接。支持 replay 和 diff。

## Project Classification

**Technical Type:** developer_tool（Kubernetes Operator + OSS 平台）
**Domain:** general（开发者效率 / DevOps 自动化）
**Complexity:** medium（K8s Operator 工程复杂度中等，无受监管领域合规负担）
**Project Context:** Greenfield — 新项目，无遗留系统

**核心技术栈倾向（待 architecture workflow 最终确认）：**
- **语言：** Go（与 K8s 生态一致，controller-runtime / kubebuilder）
- **形态：** Kubernetes Operator + 配套 Webhook ingress + 一个轻量 Dashboard（可选）
- **API 形态：** CRD（主要）+ HTTP（Webhook 接入）
- **AI 执行：** 每次 RoutineRun 由一个 **StatefulSet (replicas=1)** + 独立 PVC 承载，Pod 内跑 Claude Code，支持会话恢复
- **存储：** K8s etcd（CR 状态） + 每 Run 一个 PVC（工作区 / Claude Code 会话状态）+ 可选对象存储（归档）
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
- **T2 · 幂等与可观测**：同一个触发事件在 controller 重启/重放场景下**不会产生重复 RoutineRun**；每个 RoutineRun 可被 kubectl describe 出清晰的状态机与失败原因。
- **T3 · Blast Radius 控制**：默认配置下，任何一个 RoutineRun 不能：访问非声明的 Secret、写集群外资源、修改集群内 RBAC。这些限制有对应的单元/集成测试覆盖。

### Measurable Outcomes

| 维度 | 指标 | MVP 目标 | v1.0 目标 |
|---|---|---|---|
| 启动成本 | 从空集群到第一个 Routine 跑完 | ≤ 10 min | ≤ 5 min |
| 触发延迟 | cron 触发到 Pod Running 的中位值 | ≤ 30s | ≤ 10s |
| GitHub 事件到达率 | 非限流情况下从 webhook 入站到创建 RoutineRun 的成功率 | ≥ 99% | ≥ 99.9% |
| 最小调度间隔 | 对 Schedule trigger 允许的最小 cron 间隔 | 10 分钟 | 10 分钟（社区策略可放宽） |
| Run 恢复成功率 | 对已完成 Run 发起 continue 能成功继续对话的比例 | ≥ 95% | ≥ 99% |
| RoutineRun 重复率 | 同一触发产生的重复 Run 比例 | < 1% | < 0.1% |
| 并发执行 | 单集群并发 RoutineRun 数 | ≥ 20 | ≥ 500 |
| 失败可诊断率 | `kubectl describe routinerun` 能直接定位失败原因的比例 | ≥ 90% | ≥ 99% |

## Product Scope

### MVP - Minimum Viable Product（v0.1 ~ v0.3）

目标：**"一个熟练 K8s 用户能在自己的集群里复现上游 Claude Routines 的核心用例"**。

**MVP 必须包含：**

- `Routine` CRD + Controller：定义 prompt / repo / connector bindings / triggers。
- `RoutineTrigger` CRD + Controller：三种触发器的 CRD 分型——`ScheduleTrigger`、`WebhookTrigger`、`GitHubTrigger`。
- `RoutineRun` CRD + Controller：每次触发产生一个 Run，后端使用 **StatefulSet (replicas=1) + PVC**；状态机：`Pending → Running → Succeeded/Failed/Cancelled → (可)Resuming → Running`。
- **Run 可恢复（Resume / Continue）** ：用户可对已完成或已取消的 Run 发起 `continue`，Controller 在同一 PVC 上重新 scale 起 Pod，Claude Code 读取既有会话状态继续运行。
- `ConnectorBinding` CRD：基础 Git（读 + commit + push + 开 PR）连接器，使用 K8s Secret 注入凭据。
- GitHub trigger 基础过滤能力（通过 CEL 表达式，详见 FR8）。
- 一个最小 CLI / `kubectl` 插件（可选），提供 `routines run now <routine>`、`routines continue <run>`、`routines logs <run>` 等便捷操作。
- Helm Chart 一键安装，内置 ServiceAccount / RBAC / CRD 安装；默认的 sandbox image（含 Claude Code）作为 Helm value 暴露。
- 基础审计：每个 RoutineRun 记录触发源、使用的 prompt 版本、git commit、产生的 PR URL。

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

**高潮时刻：** 第二天早上 9 点，他打开 GitHub，看到两个 Draft PR 挂在那，每个都带有修改说明、diff 解释和 test 运行结果。他花 20 分钟 review 了一下，一个直接合入，另一个他回到 Routines 里对这次 Run 执行 `kubectl routines continue <run>`，给 Claude Code 补了一句"这个 PR 的测试不够，请补充 table-driven 测试"—— 同一个 Run 的 Pod 在原 PVC 上被重新拉起，Claude Code 读取会话状态后继续往同一个分支补 commit。

**新的世界：** 两周后，Linear 里的"good-first-bug"积压从 14 个降到 3 个，张浩晚上 10 点合上电脑就是真的合上。

**Journey 揭示的能力需求：**
- 一键 Helm 安装 + 内置 RBAC；
- 声明式定义（cron + prompt + repo + connector）；
- Git 读写 ConnectorBinding + Issue Tracker ConnectorBinding；
- 默认分支前缀 `routines/*` 的 commit / 开 PR 能力；
- Run 可 **continue / resume**：同一 Run 的工作区与会话状态通过 PVC 持久化，用户可随时补充指令让 Claude Code 在原上下文继续；
- Routine 自运行结果的持久化（PR link、diff、Claude Code log）。

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
2. **配额管理**：王磊通过 `ResourceQuota` 限定 `routines` namespace 最多同时跑 10 个 RoutineRun Pod，避免 runaway cost。
3. **Connector 授权**：GitHub App 凭据只有王磊能创建，开发者只能 `connectorBindingRefs` 引用由王磊预先创建好的 Binding，不能自己写 token 进 YAML。
4. **故障排查**：某一天一个 Routine 陷入死循环（每分钟触发一次），王磊 `kubectl get routinerun -A --sort-by=.metadata.creationTimestamp`，看到异常，`kubectl delete routinetrigger xxx`，死循环立即停止。

**Journey 揭示的能力需求：**
- CRD 版本化 + conversion webhook；
- Namespace 级别的 ResourceQuota 生效；
- Connector 与 Routine 解耦，Routine 只能引用（`connectorBindingRefs`）；
- 标准 K8s 诊断命令友好（status subresource、printer columns）；
- 暂停 / 停止机制（`spec.suspend: true`）。

### Journey 4：API 消费者 — 监控系统主动触发 Routine

**人物：** 某监控系统（Prometheus Alertmanager）。它不是人，但它是 Routines 的 "API consumer"。

**流程：** Alertmanager 的 webhook 配置里写了一个 Routines 的 WebhookTrigger URL。告警触发时：

```
POST /v1/triggers/webhook/alertmanager-critical
Authorization: Bearer <signing-token>
Content-Type: application/json

{ ...alertmanager payload... }
```

Routines 的 webhook ingress 收到请求后：
1. 验签 / 校验 token；
2. 根据 URL 路径定位到对应的 `WebhookTrigger` CR；
3. 创建一个 `RoutineRun`，把 payload 存入 Run 的 `spec.inputs`；
4. 返回 `202 Accepted` 以及 Run 名字 / URL，便于 Alertmanager 日志里有据可查。

**Journey 揭示的能力需求：**
- 公网可达的 webhook ingress（支持 IngressRoute / Gateway API）；
- 多种签名方案（HMAC、Bearer token、GitHub HMAC、Sentry HMAC）；
- 同步返回 Run 句柄；
- 幂等 key（相同 payload 不应触发两次 Run）。

### Journey Requirements Summary

上述 4 个 Journey 揭示的能力域可归纳为：

1. **Routine 生命周期**：声明、版本化、暂停/恢复、删除、升级。
2. **触发能力**：cron 定时、入站 webhook（多签名方案）、GitHub 事件（原生）、API 手工触发 / 立即运行。
3. **Connector / Secret 解耦**：ConnectorBinding 与 Routine 分离，Routine 只能引用已授权的 binding。
4. **执行运行时**：每次 Run 起独立 Pod（Pod 内跑 Claude Code），资源限制、超时、cancel、日志持久化；支持 `continue` 在同一 PVC 上恢复会话。
5. **审计与可观测**：Run 历史查询、每次 Run 的 prompt / 输入 / 输出 / 产物 / Git commit / PR link。
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

Routines 把 `Routine / RoutineTrigger / RoutineRun / ConnectorBinding` 沉淀成一等 K8s 对象，**并且用 StatefulSet + PVC 承载"可恢复会话"这一本来只在交互 IDE 里存在的概念**。这是一个新的建模层次 —— 没人把 "Claude Routines 的领域模型 + 会话状态" 作为 CRD 一起建模。它和 Knative 把"事件驱动的 serverless 函数"变成 CRD 的做法类似，但对象是**有状态的** Claude Code 任务。

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

> 具体字段由 `create-architecture` workflow 最终确定。这里只锁定**概念契约**。

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
  triggers:
    - scheduleTriggerRef: { name: nightly-2am }
  maxDurationSeconds: 1800
  concurrencyPolicy: Forbid           # Forbid | Replace | Allow
  suspend: false
status:
  lastRunRef: { name: nightly-issue-triage-run-20260415-0200 }
  conditions: [ ... ]
```

**Routine spec 字段总数：7 个**（prompt / repositoryRef / connectorBindingRefs / triggers / maxDurationSeconds / concurrencyPolicy / suspend）。每一个都是 Routines Controller 在调度 / 执行 / 审计时**必须**读的字段。任何不属于这三件事的概念（模型、端点、token、Agent 实现选择）都不在 schema 里。

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

**`RoutineRun`**：每次执行一个对象。`spec.inputs.text` 承载触发 payload 的 freeform 字符串（向上游保持兼容 — Claude Code 自己解析）；`status.phase`（`Pending/Running/Succeeded/Failed/Cancelled/Resumable/Resuming`）；`status.outputs`（产生的 PR URL、commit、markdown 摘要）；`status.sessionPVC` 指向持久化工作区。

**`ConnectorBinding`**：一个 connector 名称 + 使用的 K8s Secret + scope（readonly/readwrite）+ 注入方式（env vars / file mount）。Routine 必须通过 `connectorBindingRefs` 显式引用（strict opt-in，见下方 Design Divergences）。

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
| Run 可恢复 | 是（session 可继续对话） | **是**（StatefulSet + PVC） | 核心对等能力 |
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

- ✅ `Routine` / `RoutineRun` CRD + Controller（状态机完整）
- ✅ `ScheduleTrigger` / `WebhookTrigger` / `GitHubTrigger` 三种触发器
- ✅ `ConnectorBinding` + Git connector（GitHub 读写 + 开 PR）
- ✅ Sandbox image (内置 Claude Code) 由 Helm value 暴露
- ✅ Helm chart + kind quickstart
- ✅ `maxDurationSeconds` 硬上限
- ✅ concurrencyPolicy（Forbid / Replace / Allow）
- ✅ 基础幂等（GitHub delivery id、用户自定义 idempotency key）
- ✅ kubectl-friendly printer columns + status conditions
- ✅ `spec.suspend` 可热暂停
- ✅ Run resume / continue（同一 PVC 上拉起新 Pod，Claude Code 读取 session state 续聊）
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
| **Run 可恢复（continue）** | ✅ | | |
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
- **FR3**：开发者可以通过设置 `spec.suspend: true` 让一个 Routine 进入暂停状态，暂停后所有 trigger 不再产生新的 RoutineRun。
- **FR4**：开发者可以通过 `kubectl delete routine <name>` 删除一个 Routine；删除会级联清理其 trigger 的活动订阅（webhook ingress 路由、GitHub subscription 缓存），并保留历史 RoutineRun 便于审计。
- **FR5**：平台管理员可以查询某个 namespace 或集群中所有 Routine 的列表及其状态、下次触发时间、最近一次 Run 结果（通过 kubectl 的 printer columns）。

### Trigger Management

- **FR6**：开发者可以声明一个 `ScheduleTrigger`，使用标准 cron 语法与时区字段，定义周期性触发。默认策略下 admission webhook 拒绝最小间隔小于 **10 分钟** 的 cron 表达式；集群管理员可通过 Helm values 放宽或收紧此阈值。
- **FR7**：开发者可以声明一个 `WebhookTrigger`，由 Routines 自动在 ingress 上开出一个 stable HTTP 端点，支持 HMAC 签名与 Bearer token 校验。MVP 的 bearer token 直接存放于用户创建的 K8s Secret，不提供 token 轮换 / 一次性显示 / revoke 流程 — 管理员通过标准的 `kubectl edit secret` 自行 rotate。
- **FR8**：开发者可以声明一个 `GitHubTrigger`，订阅一个或多个仓库的事件（pull_request、push、issues、release、workflow_run 等）。MVP 至少支持 pull_request / push / issues / issue_comment / release 五类事件。
- **FR8a**：`GitHubTrigger` 支持通过 **CEL 表达式** 对 `filter` 字段求值，实现事件过滤。表达式在 **Playground 上下文** 中求值（见 CRD 草案章节），可用变量包括 `event.type` / `event.action` / `event.payload` / `event.deliveryID` / `event.repository` / `event.sender`。表达式为空或求值为 `true` 时触发 RoutineRun。
- **FR8b**：Routines 提供 `kubectl routines playground github-trigger <name> --payload <file.json>` 命令，让开发者在不真正触发的前提下，对一段真实的 GitHub payload 即时求值 filter 表达式，返回 true/false 以及表达式内每个子条件的求值结果。
- **FR9**：单个 Routine 可以绑定多个触发器（例如同时有 `ScheduleTrigger` 和 `GitHubTrigger`），任一触发器触发都会创建 RoutineRun。
- **FR10**：开发者可以通过 `routines run now <routine>` 或等价 API 手工触发一次立即运行，用于测试或紧急任务。
- **FR11**：Routines 保证对每一个入站事件的幂等处理 — 相同的 GitHub delivery id 或用户自定义 idempotency key 在配置窗口内只会触发一个 RoutineRun。

### Run Execution & Lifecycle

- **FR12**：每次触发会创建一个 `RoutineRun` 对象。Run 拥有状态机：`Pending → Scheduled → Running → (Succeeded | Failed | Cancelled) → [Resumable] → Resuming → Running → ...`。`Resumable` 意味着 Run 的工作区 PVC 仍存在、可被 `continue` 命令重新激活。
- **FR13**：每个 RoutineRun 的执行后端是一个 **StatefulSet (replicas=1) + 专属 PVC**。PVC 承载三类状态：(a) 已 checkout 的 repo 工作区；(b) Claude Code 会话状态目录；(c) 产物输出目录。Run 进入终态后 StatefulSet 缩容至 replicas=0，PVC 按保留策略保留。
- **FR13a · Run 可恢复（Resume / Continue）**：用户可以对一个处于 `Succeeded` / `Failed` / `Cancelled` / `Resumable` 的 Run 发起 `kubectl routines continue <run>`（或等价 API），可选携带一段新的 freeform 指令文本；Controller 将：
  1. 把新指令写入同一 PVC 的输入文件；
  2. 将 StatefulSet 扩容回 replicas=1；
  3. 新 Pod 挂载原 PVC，Claude Code 读取既有会话状态 + 新指令后继续运行；
  4. 原 RoutineRun 对象的 `status.phase` 进入 `Resuming → Running`，`status.history` 追加一条新条目。
- **FR13b**：PVC 保留策略可通过 `Routine.spec.runRetention` 字段配置：`keepAll` / `keepLast:N` / `ttl:<duration>`（默认 `ttl: 7d`）。到期的 PVC 被 Controller 回收，对应 RoutineRun 转为终态、不可再 continue。
- **FR14**：RoutineRun 支持 `.spec.inputs.text`（freeform 字符串）承载触发 payload —— 来自 webhook body、GitHub event JSON（整体序列化为字符串）或手工触发的自定义输入。**Routines 不对 payload 做结构化解析**，Claude Code 自行理解；这保持与上游语义的向前兼容。
- **FR15**：平台管理员和 Routine 所有者可以通过 `kubectl delete routinerun` 或 `routines cancel <run>` 取消一个正在运行的 Run；Controller 会向 StatefulSet 发送终止信号并把状态写成 `Cancelled`；PVC 按保留策略保留以便后续 continue。
- **FR16**：Routines 为每个 Run 强制执行 `maxDurationSeconds` 硬上限，超时后 Run 进入 `Failed` 并在状态中记录超时原因。`continue` 继承原 Run 的 maxDurationSeconds（继续消耗剩余时间预算）。
- **FR17**：Routines 根据 `concurrencyPolicy` 控制同一 Routine 的并发行为：`Forbid`（等待旧 Run 结束）/ `Replace`（取消旧 Run）/ `Allow`（并发执行）。`continue` 不受 concurrencyPolicy 限制（它复用同一个 Run）。

### Sandbox Runtime (Claude Code)

- **FR18**：Routines 在执行 Pod 内使用一个 sandbox image 运行 Claude Code。Sandbox image 通过 Helm value `agentImage` 配置（默认 `ghcr.io/a2d2-dev/sandbox:latest`），用户可替换为自构建版本。
- **FR19**：Controller 在拉起 Pod 时按以下约定准备工作环境：
  - 把 repo checkout 到 PVC 中的工作目录，作为 Pod 的 `WORKDIR`；
  - 把 Routine 的 prompt 写入 PVC 中的固定文件路径；
  - 把 ConnectorBinding 引用的 Secret 按其声明（env vars 或 file mount）注入 Pod —— 这是 Claude Code 读取 `ANTHROPIC_BASE_URL` / `ANTHROPIC_AUTH_TOKEN` / `ANTHROPIC_MODEL` 等标准 env vars 的来源；
  - 把 Claude Code 的 session 目录指向 PVC 上的持久化路径，使 `continue` 可以无损恢复；
  - 入口命令默认调用 `claude`，由 sandbox image 自身保证可执行（用户自构建 image 时需保留这个约定）。
- **FR20**：退出码语义：`0` = Succeeded；非零 = Failed；`75` (EX_TEMPFAIL) = Resumable（Claude Code 主动声明"我还可以被 continue"）。

### Connector & Secret Management

- **FR21**：平台管理员可以声明一个 `ConnectorBinding` CR，把 K8s Secret 与一个具名 connector（例如 `github`、`linear`、`slack`、`anthropic`）绑定，并指定 scope（readonly / readwrite）和注入方式（env / file）。
- **FR22**：Routine 通过 `connectorBindingRefs` 显式引用 ConnectorBinding（**strict opt-in**，与上游 opt-out 模型刻意不同）；没有引用的 Connector 在执行 Pod 内不可访问。
- **FR23**：Routines 保证 Routine 作者（非平台管理员）不能直接读取 Connector 所引用的 K8s Secret 明文（只能通过 Binding 引用）。
- **FR24**：MVP 至少内置一个 Git / GitHub Connector，支持 clone、commit、push、开 PR、评论 PR、评论 Issue 的能力。**MVP 默认 push 不限制分支前缀**（移除安全限制以简化 MVP）；推荐示例和文档中引导用户使用 `routines/*` 前缀作为约定俗成的 routine 工作分支命名。

### Audit, Observability & Diagnostics

- **FR25**：每个 RoutineRun 在其 `status` 中记录触发来源、使用的 prompt 哈希、使用的 sandbox image digest、执行起止时间、退出原因。
- **FR26**：每个 RoutineRun 在其 `status.outputs` 中记录产生的外部工件（PR URL、commit SHA、评论 URL、Slack 消息 ts 等）。
- **FR27**：`kubectl describe routinerun <name>` 能直接展示失败原因（含 Claude Code 退出码、最后若干行日志摘要、超时提示等）。
- **FR28**：RoutineRun 执行日志持久化到可配置后端（MVP：PVC；Growth：S3 兼容），保留策略可配置。
- **FR29**：平台管理员可以查询某个时间窗口内所有 RoutineRun 的汇总（成功率、失败原因分布、运行时长分布）。

### Security & Blast Radius

- **FR30**：Routines 的默认 install 以最小权限 RBAC 运行，core controller 不持有集群级别 `cluster-admin` 权限。
- **FR31**：Routine 执行 Pod 默认禁止 host network、host path 挂载和 privileged 模式；默认以非 root UID 运行。
- **FR32**：Routines 拒绝 Routine 访问任何未通过 ConnectorBinding 声明的 K8s Secret；尝试直接引用外部 Secret 的 Routine 在 admission 阶段被拒绝。

### Installation & Dev Experience

- **FR33**：Routines 可通过官方 Helm chart 一次命令安装，Chart 自动注册 CRD、创建 namespace、部署 controllers 和 webhook ingress；Chart 暴露 `agentImage` value 让用户覆盖默认 sandbox image。
- **FR34**：Routines 提供 `kind` / `k3d` 本地 quickstart 脚本，让新用户在本机无需真实云集群即可体验第一个示例 Routine。
- **FR35**：Routines 提供一个可选 CLI `kubectl-routines`（作为 kubectl plugin），支持 `list`、`logs`、`run now`、`cancel`、`continue`、`describe`、`playground` 等便捷命令。

### Growth / Post-MVP（非 MVP 必交付）

- **FR36**：开发者可以通过 Web Dashboard 只读查看 Routine 列表、Run 列表、Run 日志和产物。
- **FR37**：Routines 支持 GitLab 与 Bitbucket 作为事件源与 Git Connector。
- **FR38**：平台管理员可以通过 `RoutineTemplate` CR 分发可参数化的 Routine 模板，开发者只需填少量字段即可实例化。
- **FR39**：Routines 支持 OPA / Gatekeeper 策略，集群管理员可以对 Routine 施加策略约束（最大 maxDurationSeconds、允许的 Connector、允许的 sandbox image registry 等）。
- **FR40**：Routines 支持 Event Mesh：一个 Routine 的成功完成可以发出集群内事件，另一个 Routine 的 trigger 可以订阅该事件，形成 DAG 编排。
- **FR41**：可选的 token / 美元预算 sidecar，挂载到 RoutineRun Pod 中，承担 LLM 计费可观测性与超额熔断。

---

## Non-Functional Requirements

> 本节只列出对 Routines 实际产生约束的质量属性。无关类目（例如 UI 可访问性、传统 B2C 相关 SEO）已省略。

### Performance

- **NFR-P1 · 触发延迟**：从触发事件到达到执行 Pod 进入 `Running` 状态，MVP 目标中位延迟 ≤ 30 秒 / P99 ≤ 90 秒；v1.0 目标中位 ≤ 10 秒 / P99 ≤ 60 秒。（与 Measurable Outcomes 表一致。）
- **NFR-P2 · Controller Reconcile**：单 Routine Controller 实例在负载 1,000 个 Routine 稳态下，reconcile 队列长度 < 50、P99 reconcile 时间 < 500ms。
- **NFR-P3 · Webhook 入站吞吐**：Webhook ingress 单副本支持 ≥ 100 req/s 的验签 + 入队吞吐，响应 P99 < 200ms（不计 Run 执行）。
- **NFR-P4 · Run 启动成本**：Pod 启动到 Claude Code 进程开始处理 prompt 的额外开销（repo checkout / 凭据注入 / PVC mount）P50 ≤ 10 秒、P99 ≤ 30 秒。
- **NFR-P5 · Run 恢复成本**：对已终态的 Run 发起 `continue` 到新 Pod Running 的额外开销（PVC 重 mount + Claude Code 读取 session state）P50 ≤ 15 秒、P99 ≤ 45 秒。

### Reliability

- **NFR-R1 · Controller HA**：Routines core controller 支持 leader election + 多副本热备，单副本崩溃不影响正在运行的 Pod，故障切换 RTO ≤ 30 秒。
- **NFR-R2 · 事件不丢**：入站 webhook / GitHub 事件在 controller 或 ingress 短暂不可用时可缓冲至少 15 分钟的事件而不丢失（基于队列 / PVC）。
- **NFR-R3 · 幂等保证**：同一触发事件（以 delivery id / idempotency key 为主键）在 24 小时窗口内最多生成 1 个 RoutineRun，重启、重放场景下保持该语义。
- **NFR-R4 · 升级兼容**：v0.x 的 Helm upgrade 不得破坏已经在集群中运行的 `Routine` CR；CRD schema 变更必须走 conversion webhook 或 new API version。
- **NFR-R5 · Run 隔离**：单个 Run 的崩溃、死锁或超时不得影响同一 Routine 的下一次 Run 或其他 Routine。

### Security

- **NFR-S1 · 最小权限**：Routines core controller 所需的 ClusterRole 只包含对自身 CRD 的读写和对 Pods/Jobs/Secrets（限定 namespace）的管理权限，严禁 `*` 级通配 RBAC。
- **NFR-S2 · Secret 隔离**：Routine 执行 Pod 只能读取通过 `ConnectorBinding` 显式引用的 Secret；任何尝试直接 mount 集群其他 Secret 的 Routine 会在 admission webhook 阶段被拒绝。
- **NFR-S3 · 签名校验**：所有入站 Webhook 默认要求签名校验（HMAC 或 Bearer token），未通过校验的请求直接 401，不进入后续处理。
- **NFR-S4 · Pod 运行时加固**：执行 Pod 默认运行在 `runAsNonRoot: true`、`readOnlyRootFilesystem: true`（PVC 工作区除外）、`allowPrivilegeEscalation: false`、禁用 host network / host path 的安全上下文中。
- **NFR-S5 · Supply Chain**：官方发布的 controller / webhook image 必须带 SBOM 和 cosign 签名；Helm chart 也必须签名；默认 sandbox image 同样签名并发布 SBOM。
- **NFR-S6 · Prompt Injection 缓解**：默认 Connector 仅授予必要 scope（例如 GitHub token 不授予 repo admin、org admin 权限）；文档与 sample 提供"安全默认"的 ConnectorBinding 模板。
- **NFR-S7 · 审计完整性**：RoutineRun 状态和产生的外部副作用（PR、commit、评论）在删除 Run 后仍可通过外部日志 / 对象存储归档查到，保留策略可配置但默认 ≥ 90 天。

### Scalability

- **NFR-SC1 · 集群内规模**：单集群 Routines 部署支持 ≥ 1,000 个 `Routine`、≥ 500 个并发 `RoutineRun`（v1.0 目标；MVP：≥ 200 / ≥ 20）。
- **NFR-SC2 · 水平扩展**：Webhook ingress 与 Run executor 可通过增加副本线性扩展；Controller 通过 leader election 做 active-standby（不做 sharding，直到有明确 scale pain point）。
- **NFR-SC3 · 资源隔离**：每个执行 Pod 必须声明 requests / limits；Routines 对默认值提供安全下限（CPU/memory），避免无声明 Pod 踩踏共享节点。

### Integration

- **NFR-I1 · K8s 版本兼容**：MVP 支持上游 K8s 1.27+、k3s / k3d、EKS、GKE、AKS；每个 release 声明 test matrix。
- **NFR-I2 · Sandbox image 兼容**：默认 sandbox image 跟随 Claude Code 上游版本节奏发布；用户使用 image digest pinning 锁定版本以防上游变更。Routines 自身不解析 Claude Code 的输出格式（只看退出码 + PVC outputs 文件），保持松耦合。
- **NFR-I3 · 观测接入**：Routines 暴露 Prometheus metrics（reconcile 延迟、run 成功率、运行时长分布等），并且 RoutineRun 的状态变更发出 K8s Events 便于既有监控接入。
- **NFR-I4 · 日志接入**：Run Pod 的 stdout/stderr 走标准 K8s 日志机制，便于用户现有 Loki / EFK / CloudWatch 管道抓取。

### Maintainability (OSS-specific)

- **NFR-M1 · 代码质量**：Go 代码通过 `go vet`、`golangci-lint`、`staticcheck`；核心 controller 单测覆盖率 ≥ 70%。
- **NFR-M2 · E2E 测试**：每个 release 必须通过基于 envtest + kind 的端到端测试，覆盖"创建 Routine → 触发 → Run 完成"的主路径。
- **NFR-M3 · 文档同步**：文档站（Docusaurus / mkdocs 等）的内容由 repo 中 markdown 生成，任何 API / CRD 字段变更必须同一 PR 更新文档；CI 做 "docs drift" 检查。
- **NFR-M4 · Release cadence**：alpha 阶段至少每月一次 minor release；v0.1 之后公告 breaking change 至少领先 2 个版本。
