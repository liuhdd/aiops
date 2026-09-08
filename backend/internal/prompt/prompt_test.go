package prompt

import (
	"strings"
	"testing"
)

func TestChatAnswerSystemContainsCoreRules(t *testing.T) {
	for _, kw := range []string{"暂无相关信息", "禁止编造", "禁止使用自身知识补充", "算力中心硬件运维助手", "分点回答"} {
		if !strings.Contains(ChatAnswerSystem, kw) {
			t.Fatalf("ChatAnswerSystem 应含 %q: %s", kw, ChatAnswerSystem)
		}
	}
}

func TestChatAnswerUserKeepsStructure(t *testing.T) {
	u := ChatAnswerUser("alarm-1", "db-01", "disk_usage", "磁盘空间不足", "清理日志", "【知识库】\n块A", "为什么")
	for _, kw := range []string{"告警 alarm-1", "设备=db-01", "指标=disk_usage", "已有根因：磁盘空间不足", "已有处置：清理日志", "【相关上下文】", "【知识库】", "用户问题：为什么"} {
		if !strings.Contains(u, kw) {
			t.Fatalf("ChatAnswerUser 应含 %q:\n%s", kw, u)
		}
	}
}

func TestKnowledgeIntroContainsUsageRules(t *testing.T) {
	for _, kw := range []string{"标注来源", "无相关信息时", "以下是当前任务相关的上下文信息"} {
		if !strings.Contains(KnowledgeIntro, kw) {
			t.Fatalf("KnowledgeIntro 应含 %q: %s", kw, KnowledgeIntro)
		}
	}
}

func TestRCASystemContainsGapRule(t *testing.T) {
	for _, kw := range []string{"gaps", "禁止编造 evidence_chain", "只输出JSON", "算力中心硬件运维专家"} {
		if !strings.Contains(RCASystem, kw) {
			t.Fatalf("RCASystem 应含 %q: %s", kw, RCASystem)
		}
	}
}

func TestRecommendSystemContainsRunbookRule(t *testing.T) {
	for _, kw := range []string{"无预案参考", "runbook_ref", "只输出JSON"} {
		if !strings.Contains(RecommendSystem, kw) {
			t.Fatalf("RecommendSystem 应含 %q: %s", kw, RecommendSystem)
		}
	}
}

func TestQueryRewriteInstructionKeepsEntities(t *testing.T) {
	p := QueryRewriteInstruction("历史行", "磁盘满了怎么办")
	for _, kw := range []string{"原样保留", "历史：历史行", "查询：磁盘满了怎么办", "仅输出改写句"} {
		if !strings.Contains(p, kw) {
			t.Fatalf("QueryRewriteInstruction 应含 %q: %s", kw, p)
		}
	}
}
