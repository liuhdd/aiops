# 前端页面美化（AntD 深色 O&M 风格）设计

## 背景与目标

当前前端是纯 React + 全局 inline style，无任何 UI 框架与 CSS 文件：`App.tsx` 顶部按钮导航 + 内容区，运维面板三栏（告警表 / 排查时间线 / 聊天），意图管理与知识库为原生控件页面。整体观感朴素、控件风格不统一、无主题体系。

本设计引入 **Ant Design v5** 将全站（运维面板 / 意图管理 / 知识库）统一为**深色 O&M（运维）风格**：顶部 Header + 左侧 Sider 导航 + 内容区布局，全部页面组件化，通过 `ConfigProvider` 深色算法 + 自定义 token 建立全局主题体系。

**目标**：全站视觉统一、深色科技感；组件化后可维护；不改任何后端接口与数据结构（`types.ts`、`api/client.ts` 不动）。

**非目标**：不引入路由库（保持 view state 切换）；不做深浅主题切换；不新增后端接口；不实现暗色下表单/表格的新交互逻辑（仅视觉与组件化）。

## 依赖与主题

新增依赖：

- `antd`（v5，兼容 React 18）
- `@ant-design/icons`

`main.tsx` 用 `ConfigProvider` 包裹 `<App />`：

- `theme.algorithm = theme.darkAlgorithm`
- 自定义 token：`colorPrimary` `#1677ff`、圆角 `6`、紧凑度保持默认；`components` 针对 `Layout`、`Table`、`Card`、`Menu` 微调背景色分层（Header/Sider `#141a26`，Content `#0f1420`，卡片 `#1c2333` 系）

新增全局样式 `src/index.css`（`main.tsx` 引入）：

- CSS 变量定义色板；重置默认样式（`body` 背景/字体）
- 暗色滚动条、表格斑马纹、选中行高亮、antd 在暗色下的细节微调
- `index.html` 加 `<meta name="color-scheme" content="dark">` 防原生控件白闪

## 应用框架布局（`App.tsx`）

重构为 antd `Layout`：

- **Header**：左侧 Logo 区 + 标题「AIOps 硬件智能运维」
- **Sider**（可折叠）：`Menu` 三项——运维面板 / 意图管理 / 知识库，替代现有顶部按钮导航；选中高亮
- **Content**：统一 padding（16px），渲染当前 view；`view` state 切换逻辑不变

运维面板三栏工作区保持不变（左告警列表 / 中排查时间线 / 右聊天），每栏用 `Card` 承载，间距统一 16px，聊天栏固定宽 360px。

## 运维面板

### 统计卡片（数据从 `alerts` 派生，无新接口）

顶部一排 `Card` + `Statistic`：

- 告警总数
- 严重级别分布（critical / warning / info 分别计数，带主题色 Tag/图标）
- 排查中（存在 run 且状态 running）
- 未排查

### 告警列表（`AlertList` → `Table`）

- 列：ID / 设备 / 指标 / 级别（`Tag`：critical 红、warning 橙、info 蓝）/ 消息 / 状态 / 时间
- 行点击选中，选中行主题色高亮；点行查看时间线的行为不变
- 空态用 antd `Empty`；`rowKey` 用 `id`

### 排查时间线（`PipelineTimeline`）

- 外层 `Card`：标题「告警 {id}」+ 运行状态 `Tag`（运行中/完成/失败/跳过/待执行）
- 每个阶段用 `Timeline` 项 + 阶段卡片：阶段名、迭代轮次、状态 `Tag`、错误信息
- RCA 结果用 `Descriptions`：根因 / 置信度 / 证据链；`gaps` 橙黄高亮提示
- evidence 阶段显示指标点数与日志条数

### 聊天面板（`ChatPanel`）

- `Card` 承载，内部自定义气泡：user 右对齐主题色、assistant 左对齐暗色卡片
- 底部 `Input.TextArea`（Enter 发送）+ `Button`，发送中 `loading`
- 空态引导语保持

## 意图管理（`IntentManager` / `IntentTree` / `IntentEditor`）

- `IntentTree` 改走 antd `Tree`：分组节点展开/收起，节点后缀操作区（启用/停用、+子节点、编辑、删除带 `Popconfirm`）
- 节点带类型 `Tag`（分组/内置/知识库/MCP）与启用状态标识
- `IntentEditor` 改为 `Modal` + `Form`：字段按类型条件渲染（与现逻辑一致），名称必填校验，保存/取消按钮
- 删除/状态变更保留现 API 调用，成功后 `reload`

## 知识库（`KnowledgeBase`）

- 筛选区：分类 `Select` + 厂商/型号/关键词 `Input`（替代原生控件）
- 上传区：`Card` + `Upload`（可拖拽）+ 分类/标题/厂商/型号/意图 `Select`/`Input`，上传中 `Spin`
- 文档列表改 `Table`：标题 / 分类 Tag / 设备类型 / 厂商 / 型号 / 块数 / 来源 / 操作（详情、删除带确认）
- 详情改为 `Drawer`（右侧滑出）：元数据 + 分块内容（`Tag` + 段落），替代现有内联大块区域

## 测试与迁移策略

### 测试适配（关键风险点）

- antd v5 在 jsdom 下需要 `window.matchMedia` polyfill：vitest 配置加 `setupFiles`（新建 `src/setupTests.ts`），内含 `matchMedia` 与 `ResizeObserver` stub
- 现有 6 个组件测试断言基于渲染文本/占位符，改组件后大部分断言不变，个别需按新结构微调（如 `getByPlaceholderText('输入消息...')`、`getByText('发送')`）
- 新增测试：统计卡片派生计算、告警表选中高亮、时间线空态
- 验证命令：`npm run build`（tsc + vite）与 `npx vitest run` 全绿

### 迁移策略

- 一次性全量重写各组件（移除 inline style，全部交给 antd + CSS 变量），不保留两套视觉体系
- 不新增后端接口、不改 `types.ts` 数据结构、不改 `api/client.ts`

## 验证

- `npm run build`
- `npx vitest run`
- 浏览器人工走查：`npm run dev` 启动后三个页面暗色主题、布局、选中态、上传/详情交互正常

## 设计决策与取舍

1. **AntD v5 而非 v4/Tailwind/纯 CSS**：用户明确选择组件库；v5 与 React 18 兼容、自带暗色算法、CSS-in-JS 无需额外构建配置，与 Vite 开箱即用。
2. **不引入路由库**：现有 `view` state 切换已足够，Sider 菜单仅映射到 state，避免为三个页面引入 react-router 的额外复杂度。
3. **不新增后端接口**：统计卡片全部由现有 `alerts` 列表前端派生，符合「外部数据源均已 mock、改动最小」的约定。
4. **一次性全量迁移**：避免暗色原生控件与 AntD 组件并存的割裂观感，不留双体系债务。
5. **详情用 Drawer 而非 Modal**：文档内容长，Drawer 提供更大阅读空间且不打断列表浏览。
