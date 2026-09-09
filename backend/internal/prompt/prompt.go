// Package prompt 集中管理全项目提示词：内嵌默认模板 + Phoenix Prompt Hub 运行时拉取
// （spec 2026-09-09-phoenix-prompt-management-design.md）。
// 本包禁止 import 任何 aiops/internal/* 包，供 agent/context/pipeline/retrieve/eval/kb 单向引用。
package prompt

import "fmt"

// ChatAnswerSystem answerWithLLM 系统提示（Phoenix name: chat-answer-system）。
func ChatAnswerSystem() string { return Render("chat-answer-system", nil) }

// KnowledgeIntro 组装结果 system 提示：知识使用规则（Phoenix name: knowledge-intro）。
func KnowledgeIntro() string { return Render("knowledge-intro", nil) }

// RCASystem rca 非 ReAct 分支系统提示（Phoenix name: rca-system）。
func RCASystem() string { return Render("rca-system", nil) }

// RecommendSystem 处置建议系统提示（Phoenix name: recommend-system）。
func RecommendSystem() string { return Render("recommend-system", nil) }

// ChatAnswerUser answerWithLLM 用户消息（Phoenix name: chat-answer-user）。
func ChatAnswerUser(alertID, device, metric, rca, actions, contextText, question string) string {
	return Render("chat-answer-user", map[string]string{
		"alert_id": alertID, "device": device, "metric": metric, "rca": rca,
		"actions": actions, "context": contextText, "question": question,
	})
}

// QueryRewriteInstruction 检索改写提示（Phoenix name: query-rewrite）。
func QueryRewriteInstruction(history, q string) string {
	return Render("query-rewrite", map[string]string{"history": history, "query": q})
}

// IntentRewriteSplitSystem 轻量 LLM 问题重写与多意图拆分系统提示（Phoenix name: intent-rewrite-split）。
func IntentRewriteSplitSystem() string { return Render("intent-rewrite-split", nil) }

// IntentClassifySystem 轻量 LLM 意图分类系统提示（Phoenix name: intent-classify）；
// candidates 为闭集候选列表（code+描述拼装），由 agent 侧生成。
func IntentClassifySystem(candidates string) string {
	return Render("intent-classify", map[string]string{"candidates": candidates})
}

// RCAReactSystem RCA ReAct 分支系统提示（Phoenix name: rca-react-system）。
func RCAReactSystem() string { return Render("rca-react-system", nil) }

// RCAGapHint RCA 回溯轮次提示（Phoenix name: rca-gap-hint）。
// iteration==0 是「请给结论」语义分支，文案保留在 Go 侧，不上 Phoenix（spec §4）。
func RCAGapHint(iteration int) string {
	if iteration == 0 {
		return "请基于当前证据给出根因结论。"
	}
	return Render("rca-gap-hint", map[string]string{"iteration": fmt.Sprint(iteration)})
}
