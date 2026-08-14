# 意图识别评测指标设计

## 背景与目标

现有 eval 体系对 chat 任务仅用 `IntentMatch`（意图集合全命中）+ `ReplyContent`（回复关键词）评分，缺少对意图分类能力的量化指标。本设计新增三个面向意图分类的聚合指标（正确率/可信度/召回率），并扩充 chat 评测集使指标有意义。

**目标**：在 eval 报告的 config 级 summary 中新增意图指标组，逐配置对比意图分类能力；扩充 chat 任务集覆盖"应争议/不应争议"正反例。

**非目标**：不改 `agent` 运行时逻辑；不改 `Expected` 结构（争议 ground-truth 用 `Intent=="Unknown"` 推断）；不引入新依赖。

## 指标定义

### 核心概念：争议（disputed）

- **判为争议（judgedDisputed）**：系统对该输入输出 Unknown 引导语，即某次 trial 的 `res.Intents` 全部为 `Unknown`。
- **应争议（shouldDisputed）**：ground-truth 推断为 `Expected.Intent == "Unknown"`。

### 每个 chat 任务的判定（pass@k 语义，任一 trial 命中即算）

- `correct`（分类正确）：`res.Intents` 命中 `Expected.Intents`（全部出现）或 `Expected.Intent`（出现），复用现有 `gradeChat` 的 IntentMatch 判定逻辑。
- `judgedDisputed`：`res.Intents` 全部为 `"Unknown"`（且非空）。
- `shouldDisputed`：`len(Expected.Intents) == 0 && Expected.Intent == "Unknown"`。

### 三个指标（对某配置的全部 chat 任务聚合）

| 指标 | 公式 | 含义 |
|---|---|---|
| 正确率 Accuracy | `correct 任务数 / chat 任务总数` | 意图分类正确比例 |
| 可信度 Precision | `(judgedDisputed ∧ shouldDisputed) 任务数 / judgedDisputed 任务数` | 判为争议的可靠性 |
| 召回率 Recall | `(judgedDisputed ∧ shouldDisputed) 任务数 / shouldDisputed 任务数` | 应争议被正确捕获的比例 |

### 边界

- 分母为 0（无 chat 任务 / 无 judgedDisputed / 无 shouldDisputed）：该指标记 `0`，不 panic，detail 注明。
- `chat-multi` 等 `Intents` 非空任务：`shouldDisputed` 恒 false，不参与争议分母；`correct` 按 Intents 全命中判定。

## 报告展示

### `Summary.ByConfig` 新增意图指标组

```json
"by_config": {
  "rule": {
    "pass_at_k": 0.91,
    "mean_score": 0.88,
    "stddev": 0.00,
    "saturated": false,
    "intent": {"accuracy": 1.0, "precision": 1.0, "recall": 1.0}
  },
  "llm-single": { "...": "...", "intent": {"accuracy": 0.83, "precision": 1.0, "recall": 0.67} }
}
```

- `ConfigStats` 增加 `Intent IntentStats`（JSON tag `intent`）。
- `IntentStats`：`Accuracy float64`、`Precision float64`、`Recall float64`。
- stdout 摘要逐配置打印三指标行：`[cfg] intent: acc=0.83 prec=1.00 rec=0.67`。

## 评测集扩充（`internal/eval/task.go`）

新增 chat 任务（`DefaultTasks` 的 chat 段追加），让三指标有正反例：

| 任务 ID | 输入 | Expected.Intent | 应争议 |
|---|---|---|---|
| `chat-ambiguous` | "帮我查一下原因" | Unknown | 是 |
| `chat-greeting` | "在吗？" | Unknown | 是 |
| `chat-weather-offtopic` | "明天天气好吗" | Unknown | 是 |
| `chat-status-progress` | "现在排查进度如何" | AskStatus | 否 |
| `chat-disk-reason-2` | "db-01 为什么告警" | AskRootCause | 否 |
| `chat-multi-recommend` | "alarm-1 什么原因？alarm-2 怎么处理？" | Intents=[AskRootCause, AskRecommendation] | 否 |

- 反例（`chat-ambiguous`/`chat-greeting`/`chat-weather-offtopic`）在 rule 路径天然 Unknown → judgedDisputed ∧ shouldDisputed，贡献 TP。
- `chat-weather-offtopic` 是 LLM 路径的重要判别项：本应 Unknown，若 LLM 误分类到具体意图 → 不 correct、judgedDisputed=false → 同时拉低 Accuracy 与 Recall。
- `chat-multi-recommend` 验证多意图两子问题分别命中根因/处置；`ensureAlertForChat` 需支持该任务（`alarm-1`/`alarm-2` 场景或告警种子，参见实现计划）。

## 实现位置

- 新增 `backend/internal/eval/intent.go`：`IntentStats` 类型与 `intentMetrics(tasks []Task, configs []Config, scored map[string][]ScoredTrial) map[string]IntentStats`。
- `backend/internal/eval/report.go`：`ConfigStats` 增加 `Intent IntentStats`；`BuildReport` 调用 `intentMetrics` 填充；`Summary()` 打印。
- `backend/internal/eval/task.go`：追加 6 个 chat 任务。
- `backend/internal/eval/runner.go`：`ensureAlertForChat` 支持新增任务（告警种子）。

## 测试

- `internal/eval/intent_test.go`：
  - `intentMetrics` 构造带 `ScoredTrial`（含 `res.Intents`）的任务集，断言 accuracy/precision/recall 数值。
  - 边界：无 chat 任务、无 judgedDisputed、无 shouldDisputed → 记 0 不 panic。
  - 单意图 `Intents` 为空 vs 多意图 `Intents` 非空任务的 `shouldDisputed` 判定。
- `internal/eval/report_test.go`：`BuildReport` 后 `ConfigStats.Intent` 存在且数值正确；stdout 摘要含 `intent:` 行。
- 既有 `TestRuleRegression*` 保持通过（新增反例任务不改变 rule 主任务 pass@k）。

## 验证

- `go test ./internal/eval/`
- `go build ./...`

## 设计决策与取舍

1. **争议 = Unknown 引导语**：用户选定。系统输出引导语即视为"判为争议"，与运行时 `Unknown` 分支行为一致。
2. **ground-truth 用 Intent 推断**：不加 `Expected.Disputed` 字段，`Intent=="Unknown"` 即应争议，改动最小、语义清晰。
3. **系统侧看意图集合全 Unknown**：用户选定。与"争议=输出引导语"一致（Unknown 才触发引导语）。
4. **指标放 config 级 summary**：用户选定。逐配置对比意图分类能力，与现有 pass@k 并列；不改 grader 项权重。
5. **pass@k 语义**：任务级任一 trial 命中即算，与报告现有 pass@k 口径一致。
