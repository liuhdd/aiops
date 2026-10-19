# 硬件AIOps智能体

面向算力中心的硬件运维多Agent协作智能系统：接收告警后通过多阶段Agent流水线自主排查根因，并支持Web聊天对话排查。

## 架构
Go后端（Eino编排LLM）+ 流水线（接收规范化→过滤分诊→上下文收集→证据收集→根因分析→建议，证据不足时最多回溯2轮）+ 全栈mock数据（监控/日志/CMDB/知识库）+ React前端（告警列表/排查时间线/聊天）+ SQLite持久化。

## 本地开发
后端：`cd backend && go run ./cmd/server`（RCA 需要 `LLM_API_KEY`，始终通过 LLM + ReAct 排查；缺失时服务可启动，但 RCA 明确失败）
前端：`cd frontend && npm install && npm run dev`（已配置 `/api`、`/ws` 代理到8080）

## Docker一键启动
1. `cp .env.example .env` 并填入 `LLM_API_KEY`
2. `docker compose up -d --build`
3. 打开 http://localhost:3000

## 环境变量
| 变量 | 必填 | 默认 |
|------|------|------|
| LLM_API_KEY | 是 | - |
| LLM_BASE_URL | 否 | https://api.deepseek.com/v1 |
| LLM_MODEL | 否 | deepseek-chat |
| LLM_EMBED_BASE_URL | 否 | 空（默认同 `LLM_BASE_URL`） |
| LLM_EMBED_API_KEY | 否 | 空（默认同 `LLM_API_KEY`） |
| LLM_EMBED_MODEL | 否 | text-embedding-3-small |
| LLM_EMBED_BATCH | 否 | 10（单次 embedding 请求最大条数） |
| LLM_EMBED_RETRY | 否 | 5（429/5xx/网络错误重试次数，指数退避） |
| DB_PATH | 否 | aiops.db |
| PORT | 否 | :8080 |
| RETRIEVE_REWRITE | 否 | passthrough |
| RETRIEVE_RECALL | 否 | hybrid |
| RETRIEVE_GATE | 否 | floor |
| RETRIEVE_VARIANT | 否 | 空（基础变体覆盖串） |
| RETRIEVE_VARIANT_PIPELINE | 否 | 空（pipeline 专属变体） |
| RETRIEVE_VARIANT_CHAT | 否 | 空（chat 专属变体） |
| KB_FLOW_MAX_SIZE | 否 | 8000 |
| KB_EMBED_DEPTH | 否 | 2 |
| PHOENIX_BASE_URL | 否 | 空（关闭拉取），如 http://localhost:6006 |
| PHOENIX_PROMPT_TAG | 否 | production |
| PHOENIX_PROMPT_TTL | 否 | 60 |
| PHOENIX_API_KEY | 否 | 空（非空时带 Bearer 头） |

`RETRIEVE_VARIANT*` 为「环节→实现」覆盖串，多个覆盖项以 `,` 分隔，格式 `kind=impl`（如 `rerank=none,fuse=weighted`）；合法环节为 `fingerprint`/`rewrite`/`recall`/`fuse`/`relevance`/`rerank`/`gate`，未覆盖项继承默认（baseline）。

## 测试
后端：`cd backend && go test ./...`
前端：`cd frontend && npx vitest run`

## 评测（Agent Eval）

基准评测包含 44 个声明式任务（pipeline/chat/context），统一使用 `llm-react` 配置，提供三层评分器和 pass@k 统计。

```bash
cd backend
go run ./cmd/eval run --trials 3 --configs llm-react --report eval-report.json  # 完整评测
go run ./cmd/eval ci --threshold 0.9                                         # CI 门禁
```

- 任务集基于 mockdata 已知异常，ground-truth 对齐 scenario()/日志模板。
- 评测必须配置 `LLM_API_KEY`；缺失或初始化失败时直接报错。
- RCA 的模型调用、工具循环或结果解析失败时，流水线标记为失败，停止生成建议和长期记忆。
- 完整设计见 `docs/superpowers/specs/2026-08-12-agent-evaluation-design.md`。

### 检索质量评测（rageval）

```bash
cd backend
go run ./cmd/rageval                                             # 单变体 baseline
go run ./cmd/rageval -variants "baseline;rerank=none,fuse=weighted" -o rageval-report.json
go run ./cmd/rageval -variants-file variants.json -o rageval-report.json
```

`-variants` 用 `;` 分隔多个变体、同一变体内用 `,` 分隔覆盖项（如 `baseline;rerank=none,fuse=weighted`）；`-variants-file` 读取 `Variant` 数组 JSON（`name` + `steps`/`params`，未覆盖环节继承 baseline），**只传 `-variants-file`（不带 `-variants`）时会直接运行文件内全部变体 + baseline 基准**；`-variants` 可引用文件中的具名变体。Phase 1 支持的变体参数为 `params.gate.floor`（浮点，`0 < floor <= 1`，覆盖 `RERANK_FLOOR`），未知环节/参数或非法取值会在启动时报错（fail-fast）。矩阵输出的 `ComparisonReport` 含 `meta`/`variants`/`deltas`，可经 `datasetctl experiment` 逐变体回写 Phoenix。

### Phoenix 评测数据集

先 `datasetctl push` 上传数据集，本地评测后 `datasetctl experiment` 回写实验：

```bash
cd backend
go run ./cmd/promptctl push                              # 提示词（既有）
go run ./cmd/datasetctl push --dataset all               # 数据集：agents + rag
go run ./cmd/eval run --report eval-report.json
go run ./cmd/datasetctl experiment --dataset agents --report eval-report.json
go run ./cmd/rageval -o rageval-report.json
go run ./cmd/datasetctl experiment --dataset rag --report rageval-report.json
```
