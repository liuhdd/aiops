# Agent 评测体系设计

## 背景与目标

本项目（Hardware AIOps 多智能体系统）缺少统一的评测标准。参考 [AgentGuide 评测指南](https://github.com/adongwanai/AgentGuide/blob/main/docs/02-tech-stack/agent-evaluation-complete-guide.md)（Task/Trial/Grader、pass@k、Transcript vs Outcome、三层评分器等），设计一套完整的离线基准评测体系。

**目标**：构建一组带 ground-truth 的评测任务集，跑通后对系统整体能力/回归进行量化评分，用于开发迭代与 CI 门禁。

**非目标**（本期不做）：在线实时监控评分、评测结果入库 + API 查询、前端仪表盘、YAML 任务集（非工程师参与扩展）、评测工具自研框架（参考指南建议"先选够用的，精力投在写好任务上"，本项目 Go 单栈，自建轻量 eval 包最贴合）。

## 架构总览

```
backend/
  internal/eval/
    task.go      声明式任务结构 + 内置任务集（基于 mockdata 已知异常）
    runner.go    试运行器：每 (config, task, trial) 新建临时 DB 跑完整流水线
    grader.go    三层评分器（code-based / model-based / human 校准）
    report.go    JSON 报告组装 + pass@k 统计 + 饱和度检测
  cmd/eval/main.go   CLI：run / export-samples / calibrate / ci 子命令
```

复用现有组件：`pipeline.BuildWithContext`、`mockdata`、`store.New`、`llm.Client`、`context`（ScopedStore/Completer）。不引入新的第三方依赖。

## 任务模型（Task）

对应指南"考题"要求——明确输入 + 无歧义 ground-truth：

```go
type Task struct {
    ID        string      // "cpu-hot-compute-01" 等
    Object    string      // pipeline | chat | context（评测对象）
    Alert     model.Alert // 输入告警
    Expected  Expected    // ground-truth
    Configs   []string    // rule | llm-single | llm-react；空=全部
    ChatInput string      // Object==chat 时用户输入
}

type Expected struct {
    RootCause []string // 期望根因关键词（任一命中即通过）
    Metrics   []string // 期望证据指标
    Logs      []string // 期望日志关键字
    NoGaps    bool     // 期望证据/根因无 gaps
    Runbook   string   // 期望 runbook_ref
    Intent    string   // chat: 期望意图
    ToolUse   []string // context: 期望调用的工具
}
```

### 内置任务集

#### L1 流水线主任务（5 个，对应 5 个已知异常场景 + simulator 模板）

| ID | 输入告警 | 期望 RootCause | 期望日志关键字 | Runbook |
|---|---|---|---|---|
| `cpu-hot-compute-01` | compute-01 cpu_usage warning | CPU热点进程/负载饱和 | `cpu`, `load` | cpu_usage |
| `disk-full-db-01` | db-01 disk_usage critical | 磁盘空间不足 | `no space` | disk_usage |
| `gpu-drop-compute-02` | compute-02 gpu_util critical | GPU掉卡/驱动异常 | `Xid`, `fallen off the bus` | gpu_util |
| `mem-oom-web-01` | web-01 mem_usage warning | 内存泄漏/OOM杀进程 | `OutOfMemory`, `oom` | mem_usage |
| `net-down-switch` | switch-core-01 net_in critical | 网络链路中断 | `link down` | net_in |

每个任务 `Expected.NoGaps=true`（这些场景证据充分，不应产生 gaps），`Expected.Metrics` 含对应指标。

#### L2 边界任务（1 个，评测 LLM 超越规则的能力）

| ID | 输入告警 | 说明 |
|---|---|---|
| `gpu-temp-compute-02` | compute-02 **gpu_temp** critical | **规则路径无此映射** → ruleRCA 判"未知根因"；LLM 路径应推理出"GPU温度过高/散热异常"。此任务在配置对比中价值最大 |

#### L3 Chat Agent 任务（含正反例，防"偏执"）

| ID | 输入 | 期望 Intent | 期望行为 |
|---|---|---|---|
| `chat-root-cause-db` | "为什么 db-01 磁盘满了？" | AskRootCause | 回答含根因"磁盘空间不足" |
| `chat-recommend-gpu` | "怎么处理 GPU 掉卡？" | AskRecommendation | 回答含处置动作 |
| `chat-inspect-switch` | "查一下 switch-core-01 的告警" | InspectAlert | 触发流水线，返回 RCA |
| `chat-list-alerts` | "有哪些告警" | ListAlerts | 返回告警列表 |
| `chat-irrelevant` | "今天天气如何？" | **Unknown**（反例） | **不**触发流水线、不调用 LLM 工具 |

#### L4 Context/ReAct 任务（1 个，配 react=true 时生效）

| ID | 输入 | 期望行为 |
|---|---|---|
| `react-gpu-evidence` | compute-02 gpu_util critical | ReAct 路径调用工具（query_metric/search_logs），写 scratchpad，产出 RCA |

共 **12 个任务**：pipeline 6 个（L1+L2）× 3 配置，chat 5 个（L3），context 1 个（L4）。

## Runner（含环境隔离）

- 每 trial：`os.MkdirTemp` → `store.New` → `BuildWithContext`（按 config 注入 llm/react/topK）→ 用唯一 alert.ID 调 `eng.Run`
- **每次 trial 全新 DB**：解决 triage 去重（同 device+metric 已有 completed run 会 dedup→skipped）并满足指南"环境隔离 8 项"；`mockdata.GenerateAll(42)` 进程内一次性生成，全部 trial 共享同一份数据 → 确定性强
- 收集 Transcript（stage_results 输出 JSON、tool_events、scratchpad）与 Outcome（run 状态、LTM、device_context）

## 配置对比（Config Matrix）

```go
type Config struct {
    Name  string // "rule" | "llm-single" | "llm-react"
    LLM   bool   // 是否注入 llm.Client
    React bool
    TopK  int
}
var EvalConfigs = []Config{
    {"rule",       false, false, 0},
    {"llm-single", true,  false, 3},
    {"llm-react",  true,  true,  3},
}
```

Runner 为每个 config 用 `pipeline.BuildWithContext` 重建引擎。无 `LLM_API_KEY` 时 CLI 打印警告并跳过 LLM 类 config，仍可完整跑 rule 路径（确定性、零成本、CI 可用）。

`gpu-temp-compute-02` 的 code grader 需区分路径：规则路径允许"未知根因"但该维度记低分，LLM 路径期望命中"温度/散热/过温"关键词。

## 三层评分器（Grader）

### L1 Code-based（确定性，无需 LLM）

| Grader | 检查内容 |
|---|---|
| `StageComplete` | 6 阶段全 completed、run 非 degraded |
| `RootCauseMatch` | RCA.root_cause 命中 Expected.RootCause 关键词 |
| `EvidenceMatch` | 证据含预期指标/日志、无意外 gaps |
| `RecommendMatch` | actions 非空、runbook_ref 匹配 |
| `ToolUseCheck`（context） | 工具事件存在且 status==ok |
| `OutcomeCheck` | LTM 正确写入、device_context 更新 |

### L2 Model-based（LLM 评委）

对根因表述/证据支持/建议相关性打分，**按维度拆分** + **逃生门 `INSUFFICIENT_INFO`**；无 API key 时标记 `n/a` 跳过。复用 `ctxpkg.Completer` 接口（llm 评委走同一 Completer，不引入新依赖）。

### L3 Human 校准

`export-samples` 导出 {task, transcript, LLM 判定} 供人工标注，`calibrate` 计算人-LLM 一致率，>20% 分歧报警。

每个 grader 产出 `{name, pass, score 0-1, detail}`，trial 汇总为加权分 + 二进制通过。

## 非确定性与评分汇总

- 每 (task, config) 跑 N 次 trial（`--trials`，默认 3，rule 路径固定 1 次）
- 每 trial 产出二进制 pass + 连续 score（各 grader 加权）
- 任务级汇总：`pass@k`（至少 1 次 trial 通过 = 峰值能力）、**平均分**、**stddev**（稳定性）、成功 trial 数
- config 级汇总：该 config 下所有任务的 pass@k / 平均分 / stddev，用于对比表
- 全局汇总 + 饱和度检测（整体 pass@k > 0.85 → 报告建议"补充难题"）

## 报告格式

### JSON 报告

```json
{
  "meta": {"ts", "trials", "configs": ["rule","llm-single","llm-react"], "llm_enabled": true},
  "summary": {
    "overall": {"pass_at_k": 0.82, "mean_score": 0.78, "stddev": 0.11, "saturated": false},
    "by_config": {"rule": {"pass_at_k": ..., "mean_score": ...}, "llm-single": {...}},
    "by_object": {"pipeline": ..., "chat": ..., "context": ...}
  },
  "tasks": [
    {"id": "gpu-drop-compute-02", "object": "pipeline",
     "configs": {"rule": {"pass_at_k": 1.0, "mean_score": 0.95, "trials": [...]},
                 "llm-single": {"pass_at_k": 0.67, "mean_score": 0.72, "trials": [...]}}}
  ],
  "transcripts": {"<task>-<config>-<trial>": {"run_status", "stage_results", "tool_events", "rca", "gaps"}}
}
```

### stdout 摘要

人可读的配置对比表 + 每任务 pass@k。

## CLI

```
go run ./cmd/eval                       # 等价 run --trials 3 --configs rule,llm-single,llm-react
go run ./cmd/eval run --trials 5 --configs rule --report out.json
go run ./cmd/eval export-samples --n 20 --report out.json -o samples.json   # 导出人工标注样本
go run ./cmd/eval calibrate --samples samples.json -o calib.json            # 人-LLM 一致率
go run ./cmd/eval ci                       # 固定 seed + rule 路径，返回非零退出码供 CI
```

`ci` 子命令用于 CI 门禁：默认跑 rule 路径全任务 + 少量 LLM 任务（若配置 key），pass@k 低于阈值退出码 1。

## 错误处理与边界

- **无 LLM key**：LLM 类 config 跳过并标记 `"skipped":"no_api_key"`，报告仍产出
- **trial 内部失败**（如协调器锁超时→degraded）：该 trial 记 pass=false、score=0，原因写入 detail，不中断整体
- **评分器自身异常**（LLM 评委超时）：该 grader 记 n/a，权重重新归一化，不 panic
- **时间依赖**：mockdata 一次性生成、时间基于 `time.Now`，评测进程内多 trial 共享同一份数据，避免跨 trial 状态泄漏

## 测试与验证

- `internal/eval/` 单测：任务 ground-truth 与 mockdata 一致性（断言每个任务的期望日志关键字确实存在于 mockdata 日志模板中——**防止任务漂移**）、grader 判定逻辑、pass@k 统计、报告 JSON schema
- 评测自身回归测试：rule 路径下全任务 pass@k 应 = 1.0（确定性场景完美命中），作为回归护栏
- 不引入新的第三方依赖（复用 eino/llm，评分器走现有 `ctxpkg.Completer` 接口）

## 设计决策与取舍

1. **方案选型**：采用"声明式任务 + eval 引擎"（方案 A），而非 YAML 任务集（方案 B）或极简基准测试（方案 C）。理由：本项目 Go 单栈、任务与 pipeline 强耦合、单人开发，YAML schema 层是过度设计；方案 C 无法支撑配置对比/pass@k/三层评分器/独立报告。
2. **任务集数据源**：仅基于 mockdata 已知异常（`scenario()`、日志模板），零成本且完全可控；不加自定义注入、不从真实运行记录采样（尚无积累）。
3. **评测对象全覆盖**：6 阶段流水线（核心）、聊天 Agent、上下文引擎/ReAct、配置对比，一个体系全部覆盖。
4. **非确定性处理**：重复试验 + pass@k（峰值能力）为主指标，配合平均分/stddev 反映稳定性；rule 路径确定性单次。
