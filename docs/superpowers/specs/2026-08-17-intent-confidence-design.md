# 意图识别置信度打分 + description/examples 字段设计

日期：2026-08-17
状态：待评审

## 背景与目标

当前意图识别（`backend/internal/agent/intent.go`）是「规则命中 → 硬分类」：`RuleMatch` 按叶子顺序做关键词子串匹配，命中即返回意图名；未命中时由轻量 LLM（`ClassifyWithLight`）返回确定的意图名，**全程无置信度/打分环节**。意图树节点（`intent_nodes` 表）已有 `description` 字段，但无 `examples`。

本次改动目标：

1. 为意图识别加入**置信度打分**环节：规则命中给固定分，LLM 分类让模型自评分，低置信度（<0.4）落回 `Unknown`。
2. 为每条意图叶子补充 `description`（已存在，完善种子/编辑）与 `examples`（新增）字段，二者均可在意图树管理界面修改。
3. `path`（祖先分组名链）加入 `LeafSpec`，参与匹配与 LLM few-shot。

**非目标**：不在聊天回复里展示置信度（仅内部拦截）；不改动 pipeline 六阶段；不改动意图树的 CRUD/审计/软删除语义。

## 数据模型

### `model.IntentNode` / `LeafSpec`（model/intent.go）

- `IntentNode` 新增 `Examples []string \`json:"examples"\``（JSON 数组，与 `RuleKeywords` 一致存储）。
- `LeafSpec` 新增：
  - `Examples []string \`json:"examples"\``
  - `Path string \`json:"path"\``（派生，祖先分组名链，用 `/` 连接，如 `内置意图/告警排查`）

### 存储（store/intent.go + store/store.go）

- `store.go` migrate 里加一行：`ensureColumn(s.db, "intent_nodes", "examples", "examples TEXT NOT NULL DEFAULT '[]'")`。老库自动加列，examples 默认空数组。
- `intent.go`：`intentCols` 追加 `examples`；`scanIntentNode` 解析 examples（复用 rule_keywords 的 JSON 解析方式，空串 → `[]string{}`）；`InsertIntentNode`/`UpdateIntentNode` 写 examples。
- `seedIntents`：6 个内置 SYSTEM 种子各补默认 examples（见下文「种子 examples」）。

### 服务层（intent/service.go）

- `CreateReq`/`UpdateReq` 新增 `Examples []string`；`Create`/`Update` 透传落库。
- `normalize`：`KindGroup` 分支清空 `Examples = []string{}`。
- `ActiveLeavesFrom` 派生 `Path`：沿 `parent_id` 链向上收集分组 `Name`，倒序后以 `/` 连接（顶层分组也计入）。

## 打分链路（agent/intent.go）

新增类型：

```go
type ScoredIntent struct {
    Intent     Intent
    Confidence float64 // 0~1
}
```

### 规则打分 `RuleScore(leaves, text) ScoredIntent`

- 保留 `RuleMatch`（等价 `RuleScore(...).Intent`）供向后兼容。
- 对每个活跃叶子计算匹配分：
  - 匹配词集合 = `rule_keywords` ∪ `description` 切词 ∪ `path` 切词。
  - **实体名命中**（命中词属于设备名 `mockdata.Devices()`、已知指标名 `cpu_usage/disk_usage/gpu_util/mem_usage/net_in`、或 `alarmRe` 匹配）→ 强匹配，confidence = **0.9**。
  - **主题词命中**（非实体词的子串命中）→ 中等，confidence = **0.6**。
  - 无任何命中 → 0。
- 取最高分叶子；最高分 **< 0.4** → `Unknown`（confidence=0）。

> 说明：「实体名 vs 主题词」按「命中词是否落在设备/指标/alarm 词表内」区分，复用 `extractQuery` 的词表与 `alarmRe` 正则，不新增字段。

### LLM 打分 `ClassifyWithLight(leaves, text, c) (Intent, float64)`

- prompt 注入 `description + examples`（few-shot）+ `path`，要求输出 `{"intent":"<意图名>","confidence":<0~1>}`。
- 解析失败 / intent 名非法 / `c == nil` → `(Unknown, 0)`。
- confidence < 0.4 → `(Unknown, confidence)`。

### 组合入口

- `ParseIntentWithLight(leaves, text, c) (Intent, float64)`：先 `RuleScore`；规则分 ≥ 0.4 用之；否则走 `ClassifyWithLight`。
- `ParseIntentSet(leaves, text, c, maxSplit) []ScoredIntent`：重写拆分后逐子问题 `ParseIntentWithLight`，`maxSplit` 截断不变。

## Agent 消费（agent/agent.go）

- `activeLeaves()` 不变（仍由 `intent.Service.ActiveLeaves` 提供，LeafSpec 已含 examples/path）。
- `answerOneStatus`/`answerSingle` 改用 `(Intent, float64)` 结果做分支判断，**置信度不展示**，仅用于「<0.4 落 Unknown」的拦截（由 `ParseIntentWithLight` 内部完成，agent 侧无需额外判断）。

## 前端（frontend）

- `types.ts`：`IntentNode` 增 `examples: string[]`；`IntentUpsert` 增 `examples: string[]`。
- `IntentEditor.tsx`：新增「示例(逗号分隔)」输入框（仅 `kind !== 'GROUP'` 时显示），保存时按逗号/换行切分过滤空串。`description` 已可编辑，无需改。

## 种子 examples

| code | examples |
|------|----------|
| AskRootCause | `alarm-123 什么原因`、`这台机器为什么告警` |
| AskRecommendation | `alarm-123 怎么处理`、`磁盘满了怎么办` |
| AskStatus | `排查进度如何`、`现在什么状态` |
| InspectAlert | `查一下 alarm-123`、`看看 gpu-server-01` |
| ListAlerts | `有哪些告警`、`列出所有告警` |
| Unknown | （空） |

## 测试

- `store/intent_test.go`：examples 列读写、空值 → `[]`。
- `intent/service_test.go`：examples 透传、GROUP 清空 examples、`ActiveLeavesFrom` 的 `Path` 派生。
- `agent/agent_test.go`：`RuleScore` 分档（实体 0.9 / 主题 0.6 / 无命中 Unknown）、`ParseIntentWithLight` 规则优先与 LLM 兜底、`ParseIntentSet` 返回 `[]ScoredIntent`。
- `eval/intent_test.go`：确认 `TrialResult.Intent/Intents` 口径不因返回类型变化而破坏（必要时用 `.Intent` 兼容）。
- 前端 `IntentEditor`/`IntentTree` 测试更新。

## 兼容性

- 老库自动加 `examples` 列，默认空数组；规则打分不依赖 examples（仅 LLM few-shot 用），既有识别行为不变。
- `RuleMatch`/`ParseIntent` 保留，签名不变；新增函数/返回类型不破坏 `answer` 旧路径。
