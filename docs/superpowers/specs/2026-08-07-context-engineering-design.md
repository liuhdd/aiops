# 系统上下文工程（Context Engineering）设计文档

- 日期：2026-08-07
- 状态：已确认
- 范围：一次全做——Scratchpad / Memory / Tools / Knowledge 四类上下文 + 相关性选择层

## 1. 目标

把 Agent 执行任务所需的上下文**持久化在上下文窗口之外**，并在**每次调用模型前**从所有可用信息源里挑出真正相关的部分放入窗口：

1. **临时笔记板（Scratchpad）**：任务级"工作记忆"，记录推理过程、中间结果与思考步骤，供后续步骤参考。
2. **记忆（Memory）**：短期记忆 = 当前会话/回溯轮次间的最新上下文；长期记忆 = 跨会话持久化的设备/指标历史排查经验。
3. **工具（Tools）**：工具描述、参数要求、调用历史与执行结果作为上下文；模型在合适场景选择工具（ReAct）。
4. **知识（Knowledge）**：RAG 检索外部知识库（mock 语料 + 轻量检索）。
5. **相关性选择**：确定性打分器 + 每源 TopK，只把命中的相关部分注入提示词。

## 2. 已确认决策

- **一次全做**：四个子系统 + 选择层一次设计与实现。
- **消费者**：流水线（rca/recommend 阶段）与聊天 Agent 都接入。
- **工具深度**：默认确定性证据收集 + **ReAct 循环默认开启**（LLM 可用时）；`CONTEXT_REACT=false` 时回退单次调用；无 LLM 时规则兜底不变。
- **知识深度**：mock 结构化语料（mockdata 内存）+ 轻量检索（关键词/标签打分，确定性、无网络依赖）。
- **选择机制**：确定性打分器 TopK，不引入 LLM 路由。

## 3. 架构总览

新增包 `backend/internal/context/`，五个文件、单一职责：

| 文件 | 职责 |
|------|------|
| `store.go` | `ContextStore`：上下文 SQLite 持久化 + 作用域校验 |
| `selector.go` | `Selector`：确定性相关性打分 + 每源 TopK |
| `assembler.go` | `Assembler`：把命中的上下文块组装成 system/user 提示词 |
| `tools.go` | `ToolRegistry` + `Tool`：包装 mockdata 的工具注册表与执行器 |
| `react.go` | `ReAct` 循环执行器：模型选工具 → 执行 → 回填 → 循环至 final_answer |

依赖方向：`context` →（mockdata, store, scope, model, llm）；`pipeline` / `agent` →（context）。不产生环。

核心数据流：

- 每次 run 开始，engine 建 run 级 Scratchpad；每阶段完成自动写一条笔记。
- rca/recommend 调 LLM 前，`Assembler.Assemble(Query{device, metric, severity, stage, iteration, keywords})` → `Selector` 从各源取 TopK → 拼提示词。
- ReAct（LLM 可用且 `CONTEXT_REACT` 默认开启）：模型输出工具调用 JSON → `ToolRegistry.Execute` → 结果写 `tool_events` 并回填 → 循环到 final_answer。
- run 完成：engine 把 rca/recommend 结论沉淀为长期记忆（全局）。
- 聊天 Agent：复用同一 Selector/Assembler，注入短期会话记忆 + 该设备/指标的长期排查经验 + 知识；不跑 ReAct（工具与 Scratchpad 是流水线专属）。

## 4. 数据模型

`store.migrate` 新增 3 张表。短期记忆复用现有 `messages` 表（会话无 session 概念，取最近 N 条）；知识语料放 mockdata 内存。

```sql
-- 临时工作记忆（任务级，scope=task:<run_id>）
CREATE TABLE IF NOT EXISTS scratchpad_entries (
  id TEXT PRIMARY KEY, run_id TEXT NOT NULL,
  step TEXT NOT NULL,           -- 阶段名或 ReAct 步骤号
  content TEXT NOT NULL,
  tool_name TEXT,               -- 由 write_scratchpad 工具写入时记录
  created_at DATETIME
);
CREATE INDEX IF NOT EXISTS idx_scratch_run ON scratchpad_entries(run_id, created_at);

-- 工具调用历史（任务级，scope=task:<run_id>）
CREATE TABLE IF NOT EXISTS tool_events (
  id TEXT PRIMARY KEY, run_id TEXT NOT NULL,
  tool_name TEXT NOT NULL,
  args_json TEXT NOT NULL,
  result_json TEXT NOT NULL,
  status TEXT NOT NULL,         -- ok / error
  duration_ms INTEGER,
  created_at DATETIME
);
CREATE INDEX IF NOT EXISTS idx_tool_run ON tool_events(run_id, created_at);

-- 长期记忆（全局，设备/指标历史排查经验）
CREATE TABLE IF NOT EXISTS long_term_memory (
  id TEXT PRIMARY KEY,
  device TEXT NOT NULL, metric TEXT NOT NULL,
  root_cause TEXT NOT NULL, confidence REAL,
  actions_json TEXT,            -- recommend.actions
  run_id TEXT NOT NULL,
  created_at DATETIME
);
CREATE INDEX IF NOT EXISTS idx_ltm_dev_metric ON long_term_memory(device, metric, created_at);
```

**作用域对齐现有模型**：

- `scratchpad_entries` / `tool_events`：任务级，`ContextStore` 写时校验 `run_id == task scope`，越权返回 `scope.ErrScopeDenied`（复用 `scope.go` 编解码）。
- `long_term_memory`：全局写（run 完成时由 engine 落），全局读。
- 短期记忆：读 `messages` 最近 N 条（全局）。

**知识语料**（mockdata 新增 `docs.go`）：进程内结构化文档——按设备类型/指标/故障场景组织的条目 `{id, title, device_types[], metrics[], keywords[], body}`，规模几十条，确定性、测试可复现。

## 5. Selector（相关性选择器）

统一抽象：

```go
type Chunk struct {
  Source   string            // scratchpad | short_term | long_term | knowledge
  Content  string
  Metadata map[string]string // device, metric, tags, step, created_at...
  Score    float64
}
```

`Selector.Score(chunk, q)` 三类信号加权（确定性、可测试）：

- **关键词重叠**：query 关键词（设备名、指标名、severity、阶段名、显式 keywords）与 chunk 正文/Metadata 的匹配占比。
- **标签匹配**：knowledge 条目按 device_types/metrics 打标，与 query 的设备/指标命中的标签权重最高。
- **时效衰减**：`decay = e^{-λ·age}`（半衰期约 1h），新笔记/新事件更相关。

`Selector.Select(candidates, q, opts) []Chunk`：过滤低于阈值（默认 0.15）的块，每源 TopK（默认 3），跨源按 Score 降序。

## 6. Assembler（组装器）

`Assembler.Assemble(ctx, q, opts) (*Assembled, error)`：

1. 收集候选：scratchpad 按 run_id、长期记忆按 device+metric、短期记忆按最近窗口、知识按全量语料。
2. `Selector` 挑选 → 按 Source 分组。
3. 拼装（空源的分节不输出），示例：

```
【临时笔记】 step=context: 设备快照已收集...；step=evidence: 日志命中 3 条...
【设备历史经验】 db-01 disk_usage 上次(2026-08-01): 根因=磁盘空间不足(置信度0.8)，处置=[...]
【知识库】 [metric=disk_usage] 磁盘扩容预案...
【可用工具】 query_metric(device, metric, window_minutes) - 查询监控时序...
【工具调用历史】 query_metric(db-01,disk_usage) → ok(120点); search_logs(...) → ok(3条)
【短期记忆】 user: 查一下 alarm-3 什么原因；assistant: ...
```

返回 `Assembled{System, User string, Selected []Chunk}`——窗口里只出现命中的相关部分。

## 7. ToolRegistry 与 ReAct 循环

**工具注册表**（`tools.go`）：

```go
type Tool struct {
  Name        string
  Description string
  ParamSchema map[string]string // 参数名 → 说明
  Execute     func(ctx context.Context, args map[string]any) (any, error)
}
```

注册 6 个工具（全部包装 mockdata）：`query_metric` / `search_logs` / `get_device_info` / `get_neighbors` / `search_runbook` / `write_scratchpad`（模型写笔记）。每次执行写一条 `tool_events`（任务级）。

**ReAct 循环**（`react.go`）：

```
输入: system 基础指令 + Assembled 上下文 + 工具清单 + max_steps(默认5)
循环:
  1. 调 LLM，指令只输出 JSON：{"thought":"...","tool_call":{"name","args"}}
     或 {"final_answer":{...}}
  2. 解析成功且是 final_answer → 结束
  3. 是 tool_call → ToolRegistry.Execute → 结果追加到上下文 → 步骤+1
  4. LLM 错误 / JSON 解析失败 / 连续 2 次工具错误 → 中止并返回错误（rca 落回规则兜底）
  max_steps 耗尽 → 用当前证据输出（标记 degraded 提示）
```

**触发规则**：

- LLM 可用且 `CONTEXT_REACT=true`（默认）→ rca 走 ReAct。
- `CONTEXT_REACT=false` → rca 单次调用 LLM（注入组装上下文，不做工具选择）。
- 无 LLM → 现有规则兜底（`ruleRCA`）不变。

## 8. 流水线集成

**`engine.go`**：

- Run 开始时用 task 作用域建 Scratchpad；每阶段完成后自动写一条笔记（`step=<stage_id>`，内容为阶段结论摘要，如 triage 的 priority/是否去重）。
- Run 结束时（rca 有结论即写）：`st.RCA.RootCause` + `st.Recommendation.Actions` 沉淀为 `long_term_memory`（全局），同一 device+metric 累积多条历史。
- 回溯轮（iteration>0）不重建 Scratchpad——跨轮共享，第 2 轮能看到第 1 轮笔记与工具历史（流水线侧"短期记忆"）。

**`rca.go`**：按第 7 节触发规则三档执行；evidence 阶段收集到的指标/日志先落成 `tool_events`（视为已执行 `query_metric`/`search_logs`），ReAct 上下文可见"工具调用历史"，避免重复取证、让模型能针对性补查。

**`recommend.go`**：Assembler 注入知识（runbook）+ 长期记忆；不做 ReAct（单次调用），维持确定性 runbook 兜底。

**`context.go`**：采集逻辑不变，设备快照额外写一条 Scratchpad 笔记。

## 9. 聊天 Agent 集成

**`agent.go`**：

- 从用户文本提取设备/指标关键词（复用 `extractAlertID` 匹配逻辑扩展 `extractQuery`）。
- `Assembler.Assemble` 注入：短期记忆（最近 10 条 messages）+ 长期记忆（命中设备/指标的排查经验）+ 知识（runbook）。
- LLM 可用 → 用组装上下文回答；无 LLM → 规则回答并附带长期记忆摘要（如"该设备上次排查结论：磁盘空间不足"）。

## 10. 配置 / 错误处理 / 测试

**配置**（`config.go` 新增，env 加进 `.env.example`）：

- `CONTEXT_REACT`：bool，默认 `true`。
- `CONTEXT_TOP_K`：int，默认 `3`（每源取前 K）。

**错误处理**：

- 工具执行失败 → `tool_events.status=error`，ReAct 继续（模型能看到错误结果）；连续 2 次工具错误或 LLM 错误 → 中止，rca 落规则兜底。
- ContextStore 越权写 → `scope.ErrScopeDenied`，阶段标记 failed（degraded），不 panic。
- ReAct max_steps 耗尽 → 用当前证据输出，run 标记 degraded 提示。

**测试**：

- `context/selector_test.go`：打分（关键词/标签/时效）、TopK、阈值过滤。
- `context/store_test.go`：CRUD + 作用域（任务级写其它 run 的 scratchpad 被拒）。
- `context/tools_test.go`：6 个工具对 mockdata 的真实调用（先 `GenerateAll`）。
- `context/react_test.go`：注入假 `Completer`（`llm.Client` 满足同一接口）测 ReAct 循环——工具选择、结果回填、final_answer、max_steps、错误中止。
- `pipeline`：e2e 断言 ReAct 关闭时 6 阶段照常；ReAct 开启 + 假 Completer 时产生 scratchpad 与 long_term_memory 条目。
- `agent_test`：长期记忆影响规则回复。
