# AGENTS.md

Hardware AIOps: a multi-agent system for datacenter hardware O&M. Go backend (Eino LLM orchestration) + React/Vite frontend, SQLite persistence, all external data sources mocked in-process.

## Layout & commands

Two independent apps in one repo; all commands run from their own directory.

Backend (`backend/`, Go module `aiops`):
- Run: `go run ./cmd/server` (listens on `:8080`)
- Test: `go test ./...`; one package: `go test ./internal/pipeline/`

Frontend (`frontend/`, React 18 + Vite + TS):
- Dev: `npm run dev` (proxies `/api` and `/ws` to `:8080`)
- Test: `npx vitest run` (jsdom, globals); one file: `npx vitest run src/components/PipelineTimeline.test.tsx`
- Build: `npm run build` (= `tsc -b && vite build`)

Root: `cp .env.example .env` (set `LLM_API_KEY`), then `docker compose up -d --build` (backend `:8080`, frontend `:3000` via nginx).

## Env (see `backend/internal/config`)

- `LLM_API_KEY` — without it the LLM client is `nil` and LLM stages fall back to rule-based logic; the server still runs. Tests pass `pipeline.Build(s, nil, ...)`.
- `LLM_BASE_URL`, `LLM_MODEL`, `LLM_LIGHT_BASE_URL`, `LLM_LIGHT_MODEL` (lightweight intent model), `INTENT_MAX_SPLIT` (multi-intent split cap, default 3), `DB_PATH` (default `aiops.db`, relative to backend cwd), `PORT` (default `:8080`), `SIM_INTERVAL_SECONDS` (default 20; simulator push interval).

## Architecture notes

- Pipeline: `backend/internal/pipeline/build.go` — 6 stages in order: normalize → triage → context → evidence → rca → recommend. Backtracking: if `rca` reports `Gaps` and `iteration < MaxIteration` (2), evidence/rca/recommend re-run. `engine.go` locates `evidenceIndex` and skips earlier stages on re-runs — keep stage order and this index logic in sync when adding stages.
- Each stage persists a `StageResult` (JSON output via `model.MustJSON`); the frontend renders these as a timeline. `triage` dedup: same device+metric with an existing completed run marks the run `skipped`.
- Data isolation: `internal/scope` defines global ⊃ device ⊃ task levels, write-isolation + read-upward. Pipeline stages must read/write via `st.Store` (a `*store.ScopedStore` bound to the task scope), never the raw `*store.Store`. A task-scope write to another run returns `scope.ErrScopeDenied`. Device-scoped shared state lives in the `device_context` table.
- Concurrency: `internal/coordinator` enforces a per-device serial lock. `engine.Run` holds it for the whole run (30s timeout); on timeout the run is marked `degraded`. The chat agent, simulator, and API use the raw store (global scope).
- Chat agent (`internal/agent`): LLM multi-intent recognition (rewrite+split via light model, rule-first + light-LLM fallback per sub-question, aggregated reply with per-sub status); mentioning an alert ID triggers the pipeline on demand.
- Context engineering: `internal/context` 包实现 Scratchpad/记忆/工具(ReAct)/知识(RAG)，每次调 LLM 前经 Selector+Assembler 挑选相关上下文；`CONTEXT_REACT`(默认1) 控制 ReAct。

## Gotchas

- SQLite driver is `modernc.org/sqlite`; the driver name in `sql.Open` is `"sqlite"` (not `sqlite3`).
- `store.New` sets `SetMaxOpenConns(1)` because SQLite is single-writer (avoids `SQLITE_BUSY`) — don't add connections or open extra handles.
- `mockdata.GenerateAll(seed)` is one-shot per process (guarded by a `generated` flag); tests must call it before querying metrics/logs.
- Comments, docs, and commit messages are written in Chinese. Commit subjects use conventional prefixes (`feat:`/`fix:`/`test:`/`docs:`/`chore:`).
- Feature design specs and implementation plans live in `docs/superpowers/` (`specs/` and `plans/`); read the relevant spec before changing the pipeline or data model.

## Development workflow

新功能/改动默认遵循 superpowers 四阶段流程，除非明确要求不用；产物落在 `docs/superpowers/`，实现全程在隔离 worktree 进行：

1. **brainstorming** — 先探索现有代码与相关 spec，逐个澄清需求/口径，提出 2-3 方案+推荐，分节展示设计经用户确认，产出 spec 到 `docs/superpowers/specs/YYYY-MM-DD-<topic>-design.md`，自审后提交、用户复审。
2. **writing-plans** — spec 确认后写实现计划到 `docs/superpowers/plans/YYYY-MM-DD-<feature>.md`；按可独立测试的最小单元拆任务，每个任务用 TDD 步骤（写失败测试→确认失败→实现→确认通过→提交），代码完整无占位符。
3. **subagent-driven-development** — 建隔离 worktree（`.worktrees/<feature>`，分支 `feat/<feature>`），逐任务派发实现 subagent + 任务评审（spec 合规 + 代码质量），评审不通过进入修复循环；全部完成后整分支评审。进度记录在 gitignored 的 `.superpowers/sdd/<plan>/progress.md`。
4. **finishing-a-development-branch** — 全量测试绿后，由用户选择合并方式（本地合并/PR/保持）；合并后复测，删除 worktree 与分支。

约定：feature 分支名 `feat/<name>`；commit 中文 + conventional 前缀；`.env` 密钥绝不提交（`.env` 已 gitignore）。
