# AGENTS.md

Hardware AIOps: a multi-agent system for datacenter hardware O&M. Go backend (Eino LLM orchestration) + React/Vite frontend, SQLite persistence, all external data sources mocked in-process.

## Layout & commands

Two independent apps in one repo; all commands run from their own directory.

Backend (`backend/`, Go module `aiops`):
- Run: `go run ./cmd/server`（`:8080`）
- Test: `go test ./...`；单包 `go test ./internal/pipeline/`；单个用例 `go test ./internal/pipeline/ -run TestEngineBacktracksOnGaps -v`
- 静态检查：只有 `go vet ./...`（仓库未配 golangci-lint，不要假设存在 `make lint`）
- 端到端：`internal/e2e/e2e_test.go` 无 build tag，随 `go test ./...` 一起跑
- 评测：`make eval`（= `go run ./cmd/eval run --trials 3 --configs rule,llm-single,llm-react --report eval-report.json`）；CI 门禁 `make eval-ci`（阈值 0.9）
- 检索质量评测：`go run ./cmd/rageval -golden testdata/golden_set.jsonl -generate -enhance`（`-generate` 出题、`-enhance` 富化）

Frontend (`frontend/`, React 18 + Vite + TS):
- Dev: `npm run dev`（Vite 代理 `/api`、`/ws` 到 `:8080`）
- Test: `npm test`（= `vitest run`，jsdom + globals）；单文件 `npx vitest run src/components/PipelineTimeline.test.tsx`
- Build: `npm run build`（= `tsc -b && vite build`）；预览 `npm run preview`
- 无 ESLint/Prettier，不要尝试 `npm run lint`

Root: `cp .env.example .env` (set `LLM_API_KEY`), then `docker compose up -d --build` (backend `:8080`, frontend `:3000` via nginx).

## Env

唯一权威来源是 `backend/internal/config/config.go`（`LoadFromEnv`）；新增变量必须同时更新 README 的环境变量表格。

- 布尔开关统一用 `0` / `false` 关闭，默认全部开启。
- `LLM_API_KEY` 为空时 LLM client 为 `nil`，LLM 阶段自动走规则兜底，服务照常启动；测试传 `pipeline.Build(s, nil, ...)`。
- 分组：LLM（`LLM_*`）、轻量意图模型（`LLM_LIGHT_*`）、Embedding（`LLM_EMBED_*`）、上下文（`CONTEXT_REACT` / `CONTEXT_TOP_K`）、检索（`RETRIEVE_FINGERPRINT` / `RETRIEVE_RRF` / `RERANK_*`）、知识库富化（`KB_ENHANCE*` / `KB_CDTA_THRESHOLD`）、运行（`DB_PATH` / `PORT` / `SIM_INTERVAL_SECONDS` / `INTENT_MAX_SPLIT`，默认 `aiops.db`、`:8080`、20s、3）。

## Architecture notes

- Pipeline: `backend/internal/pipeline/build.go` — 6 stages in order: normalize → triage → context → evidence → rca → recommend. Backtracking: if `rca` reports `Gaps` and `iteration < MaxIteration` (2), evidence/rca/recommend re-run. `engine.go` locates `evidenceIndex` and skips earlier stages on re-runs — keep stage order and this index logic in sync when adding stages.
- Each stage persists a `StageResult` (JSON output via `model.MustJSON`); the frontend renders these as a timeline. `triage` dedup: same device+metric with an existing completed run marks the run `skipped`.
- Data isolation: `internal/scope` defines global ⊃ device ⊃ task levels, write-isolation + read-upward. Pipeline stages must read/write via `st.Store` (a `*store.ScopedStore` bound to the task scope), never the raw `*store.Store`. A task-scope write to another run returns `scope.ErrScopeDenied`. Device-scoped shared state lives in the `device_context` table.
- Concurrency: `internal/coordinator` enforces a per-device serial lock. `engine.Run` holds it for the whole run (30s timeout); on timeout the run is marked `degraded`. The chat agent, simulator, and API use the raw store (global scope).
- Chat agent (`internal/agent`): LLM multi-intent recognition (rewrite+split via light model, rule-first + light-LLM fallback per sub-question, aggregated reply with per-sub status); mentioning an alert ID triggers the pipeline on demand.
- Context engineering: `internal/context` 包实现 Scratchpad/记忆/工具(ReAct)/知识(RAG)，每次调 LLM 前经 Selector+Assembler 挑选相关上下文；`CONTEXT_REACT`(默认1) 控制 ReAct。
- Prompt 注册中心：`internal/prompt`（Phoenix 提示词注册中心：14 条注册表 + 运行时按 name+tag 拉取 + 默认模板兜底 + `cmd/promptctl` 灌库）。

### 其余核心模块

- 检索 `internal/retrieve`：混合检索管线（fingerprint 过滤 → 多路召回 → RRF 融合 → 可选 rerank，`RERANK_FLOOR` 兜底），`Searcher` 接口经 `Engine.SetSearcher` 注入流水线。
- 知识库 `internal/kb`：文档/切片/向量入库（`kb_documents`/`kb_chunks`/`kb_embeddings`）与富化（summary / questions / CDTA 相似度合并，阈值 `KB_CDTA_THRESHOLD`）；检索质量评测走 `cmd/rageval`。
- 意图 `internal/intent`：意图树 CRUD、软删除 + 启停、`intent_audit` 审计与回滚，经 `internal/api` 暴露给前端「意图管理」页。
- 推送 `internal/hub`：`GET /ws`，`Hub.Publish(type, data)` 广播 `alert`/`stage`/`run`/`message`，前端 `api/ws.ts` 订阅后局部刷新。
- 评测 `internal/eval` + `cmd/eval`：12 个声明式任务（pipeline/chat/context）× 3 配置 × pass@k，三层评分器；`ci` 子命令做门禁。
- Phoenix 评测：`internal/phoenix`（datasets/experiments 轻量 REST 客户端）、`internal/goldenset`（RAG 标注集解析/报告/Phoenix 映射）、`cmd/datasetctl`（push/list/experiment）；`cmd/rageval -o` 出结构化报告。
- 其余：`internal/simulator`（按 `SIM_INTERVAL_SECONDS` 造告警）、`internal/llm`（OpenAI 兼容封装，key 为空则 client 为 nil）、`internal/mockdata`（全量 mock 监控/日志/CMDB/知识库）。

### 持久化与接口面

- `internal/store/store.go` 启动时建表，共 15 张：`alerts`、`pipeline_runs`、`stage_results`、`messages`、`scratchpad_entries`、`tool_events`、`long_term_memory`、`device_context`、`intent_nodes`、`intent_audit`、`kb_documents`、`kb_chunks`、`kb_embeddings`、`conversations`、`kb_chunk_questions`。领域结构体在 `internal/model`，序列化统一走 `model.MustJSON`。
- HTTP 路由全在 `internal/api/server.go:routes()`：`/healthz`、`GET /api/alerts`、`GET /api/alerts/{id}`、`POST /api/alerts/{id}/inspect`（手工触发流水线）、`GET /api/messages`、`/api/conversations*`（list/create + `{id}/messages`）、`POST /api/chat`、`GET /ws`、`/api/intents/*`（tree/leaves/CRUD/status）、`/api/kb/*`（docs CRUD + upload）。统一包 `withCORS`；意图的删除/启停用请求体传 `ids`，知识库删除走 `DELETE /api/kb/docs/{id}` 路径参数。

### 前端结构

- 无路由：`App.tsx` 用 `view` state 切换三个视图（运维面板 / 意图管理 / 知识库），`ChatAssistant` 全局常驻。
- 后端交互只经过 `src/api/client.ts`（HTTP）与 `src/api/ws.ts`（WebSocket）；`src/types.ts` 是全部 DTO 的唯一来源，与 Go `internal/model` 手工同步——改 Go 结构体必须同步改它。
- 测试与组件同目录（`Foo.tsx` + `Foo.test.tsx`），公共渲染封装在 `src/test-utils.tsx`、`src/setupTests.ts`。

## Gotchas

- SQLite driver is `modernc.org/sqlite`; the driver name in `sql.Open` is `"sqlite"` (not `sqlite3`).
- `store.New` sets `SetMaxOpenConns(1)` because SQLite is single-writer (avoids `SQLITE_BUSY`) — don't add connections or open extra handles.
- `mockdata.GenerateAll(seed)` is one-shot per process (guarded by a `generated` flag). 测试建库惯用法：`store.New(filepath.Join(t.TempDir(), "t.db"))` + `mockdata.GenerateAll(seed)`，查 metrics/logs 前必须先生成。
- `backend/aiops.db` 运行后生成（已被 `.gitignore` 的 `*.db` 忽略）；`backend/testdata/` 是评测 golden set，属受控资产，勿随手改。
- 前端无 lint/format 工具链，提交前只有 `tsc -b` 兜底。
- Comments, docs, and commit messages are written in Chinese. Commit subjects use conventional prefixes (`feat:`/`fix:`/`test:`/`docs:`/`chore:`).

## Development workflow

新功能/改动默认遵循 superpowers 四阶段流程，除非明确要求不用；产物落在 `docs/superpowers/`，实现全程在隔离 worktree 进行：

1. **brainstorming** — 先探索现有代码与相关 spec，逐个澄清需求/口径，提出 2-3 方案+推荐，分节展示设计经用户确认，产出 spec 到 `docs/superpowers/specs/YYYY-MM-DD-<topic>-design.md`，自审后提交、用户复审。
2. **writing-plans** — spec 确认后写实现计划到 `docs/superpowers/plans/YYYY-MM-DD-<feature>.md`；按可独立测试的最小单元拆任务，每个任务用 TDD 步骤（写失败测试→确认失败→实现→确认通过→提交），代码完整无占位符。
3. **subagent-driven-development** — 建隔离 worktree（`.worktrees/<feature>`，分支 `feat/<feature>`），逐任务派发实现 subagent + 任务评审（spec 合规 + 代码质量），评审不通过进入修复循环；全部完成后整分支评审。进度记录在 gitignored 的 `.superpowers/sdd/<plan>/progress.md`。
4. **finishing-a-development-branch** — 全量测试绿后，由用户选择合并方式（本地合并/PR/保持）；合并后复测，删除 worktree 与分支。

约定：feature 分支名 `feat/<name>`；commit 中文 + conventional 前缀；`.env` 密钥绝不提交（`.env` 已 gitignore）。

`docs/superpowers/` 下 14 份 spec + 14 份 plan 均按 `YYYY-MM-DD-<topic>-design.md` 命名，动手改流水线、数据模型、意图、知识库、检索前先在该目录按主题检索对应 spec；`.worktrees/`、`.superpowers/`、`ragent/`（无关嵌套仓库）已 gitignore，勿碰 `ragent/`。
