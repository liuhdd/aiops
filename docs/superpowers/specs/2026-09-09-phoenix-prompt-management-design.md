# Phoenix Prompt Hub 提示词管理设计

日期：2026-09-09
状态：已确认（brainstorm 四节澄清 + 方案 A + 六节设计经用户确认）

## 1. 背景与目标

当前项目 13 条 LLM 提示词分两处硬编码：6 条集中在 `backend/internal/prompt/prompt.go`（RAG 提示词调优产物，见 `2026-09-08-rag-prompt-tuning-design.md`），7 条内联在各模块（agent 意图、RCA ReAct、eval 评委、KB 富化）。每次调优都要改代码、重新编译部署，不利于迭代。

本设计把全部 13 条提示词托管到 Arize Phoenix Prompt Hub：在 Phoenix UI 在线编辑、版本化、打 tag，运行时按 `name + tag` 拉取，Phoenix 不可用或未配置时回退内嵌默认模板。

### 与既有 spec 的关系

- `2026-09-09-llm-observability-design.md`（Langfuse 可观测性，待确认）：本设计**取代其选型**——后续 tracing 走 Phoenix/OTel 另立 spec；该 spec 中「prompt 管理不在范围」的约定由本 spec 承接。其可观测性埋点部分（`internal/observe` 等）与本设计无依赖，互不阻塞。
- `2026-09-08-rag-prompt-tuning-design.md`：`internal/prompt` 包由此而来，本设计是该包的演进（常量 → 注册中心），包的「禁止 import 其他 internal 包」约束保持不变。

## 2. 选型结论

| 候选 | 结论 |
|---|---|
| 方案 A：prompt 包重构 + 自写轻量 Phoenix REST 客户端 | **采用**。零第三方依赖（Phoenix 无官方 Go SDK，仅 Python/TS），降级路径清晰，调用点签名不变 |
| 方案 B：Phoenix 定时同步到本地 SQLite 表再读本地 | 否决。多一张表 + 同步任务，编辑后延迟生效，违背「改 Phoenix 即生效」目标，YAGNI |
| Langfuse prompt management | 否决。用户已选定 Phoenix 统一承接 prompt 管理 + 后续 tracing |

已核实的 Phoenix REST 事实（`/v1`，自托管）：

- 按 name+tag 取 prompt version：对应 REST API `get-prompt-version-by-tag`（`/v1` 下，精确路径实现时以 Phoenix 自带 OpenAPI 文档为准，路径形如 `/v1/prompts/{id}/tags/{tag}/versions`）
- `POST /v1/prompts` — 新建 prompt（含首个版本）
- prompt version 含 `messages`（role/content 数组），模板变量为 f-string 风格 `{{var}}`
- 拉取也可按 name（最新版）或 versionId；本设计只按 **name+tag**，环境发布由打 tag 驱动

## 3. 总体架构

`internal/prompt` 包升级为「提示词注册中心」，仍是全项目唯一提示词来源，不新增顶层模块：

```
调用方 (pipeline / agent / context / kb / eval / retrieve)
   │  prompt.RCASystem(...) 等函数（签名基本不变）
   ▼
prompt.Registry ── 渲染 {{var}} ──► 返回最终 string
   │ 缓存命中：Phoenix 拉取的版本（内存缓存，TTL 后台刷新）
   │ 未命中 / 未配置 / 拉取失败：内嵌默认模板（即现有常量原文）
   ▼
prompt.PhoenixFetcher ── GET /v1/prompts/{name}/tags/{tag}/versions
```

设计要点：

1. **每条提示词建模**为 `{name, 默认模板, 变量渲染函数}`。name 即 Phoenix prompt 标识（如 `rca-system`）；默认模板即现有常量/拼接逻辑原文转写为 `{{var}}` 模板，保证未接 Phoenix 时行为与现状完全一致。
2. **调用点签名不变**。现有 6 个导出常量改为同名函数（常量 → `func Xxx() string`），7 条内联提示词收编为同类函数；`llm.Client.Complete(ctx, system, user)` 不动，kb 包的 `Completer.Complete(ctx, prompt string)` 也不动。渲染用 `strings.NewReplacer` 做 `{{var}}` 替换，不引模板引擎。
3. **动态部分抽为变量**：`intent-classify` 的意图候选列表 → `{{candidates}}`（拼装逻辑留在 Go 侧 `agent` 包）；`rca-gap-hint` 的两个分支文案合并为一条模板，`iteration=0` 的分支判断留在 Go 侧（见 §4 注）。
4. **包约束不变**：`internal/prompt` 仍禁止 import 任何 `aiops/internal/*` 包；Phoenix HTTP 交互所需的配置以参数注入（`Fetcher` 结构体字段），不 import `internal/config`。

## 4. 提示词清单与变量映射（代码 13 处 → Phoenix 14 条）

| Phoenix name | 现位置 | 模板角色 | 变量 |
|---|---|---|---|
| `chat-answer-system` | `prompt.ChatAnswerSystem` | system | — |
| `chat-answer-user` | `prompt.ChatAnswerUser()` | user | `alert_id` `device` `metric` `rca` `actions` `context` `question` |
| `knowledge-intro` | `prompt.KnowledgeIntro` | system | — |
| `rca-system` | `prompt.RCASystem` | system | — |
| `recommend-system` | `prompt.RecommendSystem` | system | — |
| `query-rewrite` | `prompt.QueryRewriteInstruction()` | user | `history` `query` |
| `intent-rewrite-split` | `agent/intent.go` `RewriteAndSplit` system | system | — |
| `intent-classify` | `agent/intent.go` `ClassifyWithLight` system | system | `candidates` |
| `rca-react-system` | `pipeline/rca.go` `react` system | system | — |
| `rca-gap-hint` | `pipeline/rca.go` `extraGapHint` | user 尾部 | `iteration` |
| `eval-judge-system` | `eval/grader.go` `gradeWithJudge` system | system | — |
| `eval-judge-user` | `eval/grader.go` `gradeWithJudge` user | user | `task_id` `evidence` `rca` `rubric` |
| `kb-summarize` | `kb/enhance.go` `Summarize` | user | `content` |
| `kb-gen-questions` | `kb/enhance.go` `GenerateQuestions` | user | `content` |

注：

- 原「6 条集中 + 7 条内联」计 13 个调用点；`extraGapHint` 两分支合并、`gradeWithJudge` 的 system/user 拆为两条 Phoenix 条目后，Phoenix 侧共 **14 个 prompt 条目**（`chat-answer-user`、`query-rewrite`、`eval-judge-user`、`kb-*` 为 user 模板，其余为 system 模板）。
- `extraGapHint`：`iteration>0` 的文案改为 `rca-gap-hint` 模板（「这是第{{iteration}}轮补充取证，请重点分析新增证据。」）；`iteration==0` 的文案「请基于当前证据给出根因结论。」是另一语义分支，保留在 Go 侧硬编码（一条 if），不设第二条 Phoenix 条目。
- `rca-system` 中 `singleCall` 的 user 侧「设备/指标/证据」拼接为程序化结构，非提示词文案，不入 Phoenix，维持现状。
- user 模板（`kb-*`、`intent-rewrite-split` 等）当前以 system=""、prompt 当 user 的方式调用，收编后调用方式不变，仅文本来源改为 Registry。

## 5. Phoenix 交互与降级语义

### 拉取与缓存

- 按 `name + tag` 拉取，tag 默认 `production`，全局一个 tag（环境发布 = 给版本打 tag）。
- 进程内缓存 + TTL（默认 60s）；TTL 到期**后台刷新**，刷新期间继续用旧值，刷新失败沿用旧值并记一次 warn。
- 并发单飞：同一 name 的并发拉取合并为一次请求（用已有依赖 `golang.org/x/sync` 的 `singleflight`）。
- HTTP 超时 2s；启动不阻塞——首次调用时惰性拉取，拉不到即用默认模板。

### 降级链（与「LLM key 为空走规则兜底」同款哲学）

1. `PHOENIX_BASE_URL` 未配置 → 永远用内嵌默认模板，零网络行为（等价现状，测试路径不受影响）。
2. 配置了但请求失败 / 超时 / 响应缺 `messages` 字段 → 用内嵌默认模板。
3. 降级发生时记 warn 日志；同一 name 降级日志 1 分钟内去重，避免流水线高频调用刷屏。

### 认证与写路径

- 自托管默认无鉴权；预留 `PHOENIX_API_KEY`（非空时请求带 `Authorization: Bearer <key>`）。
- 运行时**只有 GET**；push 灌库是离线动作（§6）。

## 6. 配置、部署与灌库

### 新增环境变量（同步 README 表格 + `.env.example`）

| 变量 | 默认 | 说明 |
|---|---|---|
| `PHOENIX_BASE_URL` | 空（=关闭拉取） | 如 `http://localhost:6006` |
| `PHOENIX_PROMPT_TAG` | `production` | 拉取使用的 tag |
| `PHOENIX_PROMPT_TTL` | `60`（秒） | 缓存刷新周期 |
| `PHOENIX_API_KEY` | 空 | 非空时带 Bearer 头 |

读取遵循 `config.go` 既有惯例（`LoadFromEnv` 纯函数注入 + 缺省覆盖），TTL 非法值回落默认。

### docker-compose

新增 `phoenix` 服务：镜像 `arizephoenix/phoenix`，端口 `6006:6006`，数据卷持久化；backend 增加 `PHOENIX_BASE_URL=http://phoenix:6006`。

### `cmd/promptctl`（新命令）

- `promptctl push`：把 14 条默认模板按 §4 的 name 逐条 `POST /v1/prompts` 灌到 Phoenix（已存在的 name 新建版本），供首次部署 / 模板初始化使用。模型信息（provider/model_name）按 Phoenix 要求填占位（如 `OPENAI` / 现配的 `LLM_MODEL`），仅作元数据。
- `promptctl list`：列远端 prompt 的 name/tag/version，核对灌库结果。
- 复用 `internal/prompt` 的默认模板与 `PhoenixFetcher` 的 HTTP 封装，不复制逻辑。

## 7. 测试策略

- **Fetcher 单测**（`httptest` 假 Phoenix）：按 tag 拉取成功、响应缺字段降级、超时降级、TTL 过期后台刷新、并发单飞、`PHOENIX_BASE_URL` 为空零请求。
- **模板快照测试**：14 条默认模板各一条断言（渲染结果含预期关键字），防止 `promptctl push` 的模板与代码漂移。
- **promptctl 单测**：push 请求体断言（name/messages/变量占位完整）。
- **回归**：`go test ./...` 全绿——现有流水线 / agent / eval 测试不应感知此改动（函数签名兼容、默认模板 == 现文案）。

## 8. 明确不做（YAGNI）

- Phoenix tracing / OTel 埋点：后续把 Langfuse spec 改写为 Phoenix 版另立 spec。
- 版本回滚 UI、A/B 流量切分、前端提示词管理界面：Phoenix UI 已覆盖编辑/版本/打 tag。
- 不引 OTel SDK、phoenix-client 等第三方依赖。
- 不做按 run 维度的提示词快照存档（stage_results 已存最终拼好的 system/user 文本，天然可追溯）。
