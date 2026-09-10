# 硬件AIOps智能体

面向算力中心的硬件运维多Agent协作智能系统：接收告警后通过多阶段Agent流水线自主排查根因，并支持Web聊天对话排查。

## 架构
Go后端（Eino编排LLM）+ 流水线（接收规范化→过滤分诊→上下文收集→证据收集→根因分析→建议，证据不足时最多回溯2轮）+ 全栈mock数据（监控/日志/CMDB/知识库）+ React前端（告警列表/排查时间线/聊天）+ SQLite持久化。

## 本地开发
后端：`cd backend && go run ./cmd/server`（需要环境变量 `LLM_API_KEY`，缺失时自动用规则兜底）
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
| DB_PATH | 否 | aiops.db |
| PORT | 否 | :8080 |
| KB_FLOW_MAX_SIZE | 否 | 8000 |
| KB_EMBED_DEPTH | 否 | 2 |
| PHOENIX_BASE_URL | 否 | 空（关闭拉取），如 http://localhost:6006 |
| PHOENIX_PROMPT_TAG | 否 | production |
| PHOENIX_PROMPT_TTL | 否 | 60 |
| PHOENIX_API_KEY | 否 | 空（非空时带 Bearer 头） |

## 测试
后端：`cd backend && go test ./...`
前端：`cd frontend && npx vitest run`

## 评测（Agent Eval）

离线基准评测体系，12 个声明式任务（pipeline/chat/context）+ 3 种配置对比 + 三层评分器 + pass@k 统计。

```bash
cd backend
go run ./cmd/eval run --trials 3 --configs rule,llm-single,llm-react --report eval-report.json  # 完整评测
go run ./cmd/eval ci --threshold 0.9                                                        # CI 门禁
```

- 任务集基于 mockdata 已知异常，ground-truth 对齐 scenario()/日志模板。
- 无 `LLM_API_KEY` 时自动跳过 LLM 类配置，仍可跑 rule 路径。
- 完整设计见 `docs/superpowers/specs/2026-08-12-agent-evaluation-design.md`。
