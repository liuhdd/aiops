// Package prompt 集中管理全项目提示词：内嵌默认模板 + Phoenix Prompt Hub 运行时拉取
// （spec 2026-09-09-phoenix-prompt-management-design.md）。
// 本包禁止 import 任何 aiops/internal/* 包，供 agent/context/pipeline/retrieve/eval/kb 单向引用。
package prompt

// ChatAnswerSystem answerWithLLM 系统提示（Phoenix name: chat-answer-system）。
func ChatAnswerSystem() string { return Render("chat-answer-system", nil) }

// KnowledgeIntro 组装结果 system 提示：知识使用规则（Phoenix name: knowledge-intro）。
func KnowledgeIntro() string { return Render("knowledge-intro", nil) }

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

// EvalJudgeSystem 评测 LLM 评委系统提示（Phoenix name: eval-judge-system）。
func EvalJudgeSystem() string { return Render("eval-judge-system", nil) }

// EvalJudgeUser 评测 LLM 评委用户消息（Phoenix name: eval-judge-user）。
func EvalJudgeUser(taskID, evidence, rca, rubric string) string {
	return Render("eval-judge-user", map[string]string{
		"task_id": taskID, "evidence": evidence, "rca": rca, "rubric": rubric,
	})
}

// KBSummarizePrompt 知识库富化摘要提示（Phoenix name: kb-summarize）。
func KBSummarizePrompt(content string) string {
	return Render("kb-summarize", map[string]string{"content": content})
}

// KBGenQuestionsPrompt 知识库富化出题提示（Phoenix name: kb-gen-questions）。
func KBGenQuestionsPrompt(content string) string {
	return Render("kb-gen-questions", map[string]string{"content": content})
}
