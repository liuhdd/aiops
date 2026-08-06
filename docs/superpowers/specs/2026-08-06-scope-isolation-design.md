# 跨Agent三维度数据隔离设计文档

- 日期：2026-08-06
- 状态：已确认

## 1. 目标

解决硬件AIOps智能体中跨 Agent 数据协作的数据共享问题，通过**任务级/设备级/全局级**三维度嵌套隔离（全局⊃设备⊃任务），实现：

1. **可见性作用域**：每个 Agent 声明自己所属层级，数据读写受作用域约束。
2. **并发协调**：同一设备同一时刻只允许一个活跃排查任务（设备级串行锁）。

## 2. 已确认约束

- 三维度：任务级/设备级/全局级，**嵌套层级**（全局⊃设备⊃任务）。
- 读写规则：**写隔离 + 读向上**（方案B）。
- 纳入范围：**核心协作数据面**（alerts、pipeline_runs、stage_results、设备上下文）；聊天记录与知识库为全局只读，不参与写隔离。
- 设备级并发：**设备级串行锁**。
- 承载方式：**表加作用域字段**。

## 3. Scope 模型与权限矩阵

新增 `internal/scope` 包：

```go
package scope

type Level int

const (
	LevelGlobal Level = iota // 全局
	LevelDevice              // 设备
	LevelTask                // 任务
)

type Scope struct {
	Level    Level  // 层级
	TaskID   string // 任务级: 流水线 run ID
	DeviceID string // 设备级: 设备名 (任务级时 = 宿主设备)
}
```

- 流水线阶段 → 任务级 Scope（TaskID=run，DeviceID=alert.Device）
- 聊天 Agent / 告警模拟器 → 全局级 Scope

**权限矩阵（写隔离 + 读向上）：**

| Agent层级 | 可写 | 可读 |
|-----------|------|------|
| 任务级 | 本 run 的 pipeline_runs / stage_results | 本 run + 宿主设备上下文 + 全局知识(只读) |
| 设备级 | 本设备的 alerts / device_context | 本设备数据 + 全局(只读) |
| 全局级 | messages、全局数据 | 全部 |

规则：
- 写入严格分层：store 写方法校验 Scope 与目标行作用域匹配，不匹配返回 `ErrScopeDenied`。
- 读取向上：任务可见宿主设备+全局；设备可见全局；全局见全部。
- 跨层写（如任务级更新设备上下文）：必须经 `devicectx` 服务层，服务校验请求者宿主设备==目标设备后以设备级身份写入。
- 知识库/CMDB/聊天记录为全局只读，不参与写隔离。

## 4. 数据模型改造

**表加作用域字段**（`store.migrate` 中新增列，兼容已有数据）：

- `alerts` 增加 `scope` TEXT：值 `device:<device>`（告警天然属设备级）
- `pipeline_runs` 增加 `scope` TEXT：`task:<id>`
- `stage_results` 增加 `scope` TEXT：`task:<run_id>`
- `messages` 不改造（全局级，只读隔离外）

新增 `device_context` 表（设备级共享上下文）：
```sql
CREATE TABLE IF NOT EXISTS device_context (
    device_id   TEXT PRIMARY KEY,
    state_json  TEXT NOT NULL DEFAULT '{}',
    updated_by  TEXT,
    updated_at  DATETIME
);
```

**Scope 编码与解析**（`scope` 包）：
- `Encode(sc Scope) string` → `global` / `device:<name>` / `task:<id>`
- `Parse(s string) (Scope, error)`
- store 写入时由调用方序列化作用域；读取时按 Scope 过滤

**兼容策略**：旧行无 scope → 迁移填充 `scope=global`（视为全局可读），新写入一律带 scope。

## 5. store 层隔离与设备级串行锁

**store 改造**：
- 写方法增加 scope 校验，例如 `SaveStage(sc Scope, s model.StageResult) error`：解析目标行 scope，校验 `Level==LevelTask` 且 `s.RunID == sc.TaskID` 才允许，否则 `ErrScopeDenied`。
- 读方法按 Scope 过滤：任务级只读本 run；设备级读本设备 alerts + device_context；全局读全部。
- 新增 `store.ScopedStore(sc Scope) *ScopedStore`：绑定 Scope 的视图，读自动过滤、写自动校验。各 Agent 持有自己的 ScopedStore，无需到处传 Scope。
- `ScopedStore` 方法：`SaveStage`/`ListStages`(限定本 run)、`SaveAlert`/`ListAlerts`(设备级)、`GetDeviceContext`/`UpdateDeviceContext`(设备级)。

**设备级串行锁**（并发协调）：
- 新增 `internal/coordinator`，持 `map[string]*sync.Mutex`（按 device）：
  - `Acquire(device string) func()`：获取该设备锁（无则创建），返回释放函数
  - `TryAcquire(device string, timeout time.Duration) (func(), bool)`：排队场景，超时返回失败
- `engine.Run` 入口：按 `alert.Device` 获取锁，整个流水线执行期间持有，结束释放——同设备新告警排队串行，杜绝设备上下文并发写冲突。
- 跨设备任务互不阻塞。

**数据流**：模拟器/聊天 → store.SaveAlert(全局) → engine.Run(取设备锁 → 各阶段经 ScopedStore 读写 → 释放锁)。

## 6. Agent 接入、错误处理与测试

**Agent 接入改造**：
- `pipeline.Engine` 持有 `coordinator` 和 `store`；`Run` 内为每阶段构造 `ScopedStore(taskScope)`，注入 `State.Store *store.ScopedStore` 新字段，阶段从 `st.Store` 访问数据。
- 阶段改造：
  - `triage`：改用 `st.Store.ListAlerts()`（任务级读向上 → 可见宿主设备告警，用于去重）
  - `context`：经 `st.Store.GetDeviceContext(device)` 读设备上下文；写设备快照经 `st.Store.UpdateDeviceContext`（服务层校验宿主设备）
  - `evidence`：mockdata 指标/日志按设备查询（已是设备维度，读路径确认不动）
  - `rca`/`recommend`：知识库全局只读，读路径不动
- `agent.Agent`：全局 Scope，`Handle` 读写 messages 不受限；点名告警经 store 查 alerts（全局可读全部）
- `main.go`：装配 coordinator 传给 `pipeline.Build`；模拟器 onAlert 不变

**错误处理**：
- `ErrScopeDenied`：越权写 → 返回该错误，流水线该阶段标记 failed（degraded），不 panic。
- 设备锁获取：同设备任务排队；提供超时（30s）防死锁，超时则任务标记 degraded 并记录"设备忙"。
- 迁移旧数据无 scope → 默认 `global`，读不阻塞。

**测试**：
- scope 单元测试：Encode/Parse 往返、权限矩阵校验（任务写他 run 拒绝、设备写他设备拒绝、全局写全允许）
- store 隔离测试：ScopedStore 越权写返回 ErrScopeDenied；读过滤正确
- coordinator 测试：同设备串行（并发两任务，第二个等待第一个释放）、不同设备并行
- 端到端：e2e 断言流水线在隔离下仍产出全部 6 阶段
