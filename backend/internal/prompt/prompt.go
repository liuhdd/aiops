// Package prompt 集中管理 RAG 生成端提示词常量（三阶避坑法：核心规则前置 + 异常兜底 + 输出格式，约束总数 ≤5 条）。
// 本包禁止 import 任何 aiops/internal/* 包，供 agent/context/pipeline/retrieve 单向引用。
package prompt

import "fmt"

// ChatAnswerSystem answerWithLLM 系统提示：核心规则 + 角色（领域+身份+核心要求）+ 输出要求。
const ChatAnswerSystem = "【核心规则】必须严格基于【相关上下文】与已有排查结论回答；上下文中没有的信息回答\"暂无相关信息\"，禁止使用自身知识补充，禁止编造。" +
	"你是算力中心硬件运维助手，结论需注明依据来源（上下文段落或已有根因）。" +
	"【输出要求】分点回答，每点不超过3句，语言简洁专业。"

// KnowledgeIntro 组装结果 system 提示：知识使用规则。
const KnowledgeIntro = "以下是当前任务相关的上下文信息。回答时优先依据【知识库】【设备历史经验】等分段内容，引用结论时标注来源文档；分段中无相关信息时直接说明，不要引用不存在的段落。"

// RCASystem rca 非 ReAct 分支系统提示：补证据兜底口径。
const RCASystem = "你是算力中心硬件运维专家。基于给定证据输出JSON：{\"root_cause\":string,\"evidence_chain\":[string],\"confidence\":0~1,\"gaps\":[string]}。证据中不存在的内容必须写入 gaps，禁止编造 evidence_chain。只输出JSON。"

// RecommendSystem 处置建议系统提示：步骤须与预案对应。
const RecommendSystem = "你是算力中心运维助手。基于根因和预案，给出简明的处置建议步骤列表。步骤须与 runbook_ref 对应，无对应预案时 risk 填\"无预案参考\"。只输出JSON：{\"actions\":[string],\"runbook_ref\":string,\"risk\":string}"

// ChatAnswerUser answerWithLLM 用户消息（保持现有结构：告警行 + 已有结论 + 上下文 + 问题）。
func ChatAnswerUser(alertID, device, metric, rca, actions, contextText, question string) string {
	return fmt.Sprintf("告警 %s：设备=%s 指标=%s\n已有根因：%s\n已有处置：%s\n\n【相关上下文】\n%s\n\n用户问题：%s",
		alertID, device, metric, rca, actions, contextText, question)
}

// QueryRewriteInstruction 检索改写提示：实体词原样保留。
func QueryRewriteInstruction(history, q string) string {
	return "将下述查询结合对话历史改写为独立检索句，仅输出改写句。设备名、告警ID、指标名等实体词必须原样保留。\n历史：" + history + "\n查询：" + q
}
