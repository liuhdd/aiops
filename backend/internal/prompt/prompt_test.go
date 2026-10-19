package prompt

import (
	"strings"
	"testing"
)

func TestChatAnswerSystemContainsCoreRules(t *testing.T) {
	for _, kw := range []string{"暂无相关信息", "禁止编造", "禁止使用自身知识补充", "算力中心硬件运维助手", "分点回答"} {
		if !strings.Contains(ChatAnswerSystem(), kw) {
			t.Fatalf("ChatAnswerSystem 应含 %q: %s", kw, ChatAnswerSystem())
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
		if !strings.Contains(KnowledgeIntro(), kw) {
			t.Fatalf("KnowledgeIntro 应含 %q: %s", kw, KnowledgeIntro())
		}
	}
}

func TestRecommendSystemContainsRunbookRule(t *testing.T) {
	for _, kw := range []string{"无预案参考", "runbook_ref", "只输出JSON"} {
		if !strings.Contains(RecommendSystem(), kw) {
			t.Fatalf("RecommendSystem 应含 %q: %s", kw, RecommendSystem())
		}
	}
}

func TestAccessorsUseRegistry(t *testing.T) {
	resetRegistry(t)
	if ChatAnswerSystem() != All["chat-answer-system"].Default {
		t.Fatal("ChatAnswerSystem 未走注册表默认模板")
	}
	if KnowledgeIntro() != All["knowledge-intro"].Default {
		t.Fatal("KnowledgeIntro 未走注册表默认模板")
	}
	if RCAReactSystem() != All["rca-react-system"].Default {
		t.Fatal("RCAReactSystem 未走注册表默认模板")
	}
	if RecommendSystem() != All["recommend-system"].Default {
		t.Fatal("RecommendSystem 未走注册表默认模板")
	}
}

func TestIntentClassifySystemRendersCandidates(t *testing.T) {
	resetRegistry(t)
	got := IntentClassifySystem("AskRootCause、AskAdvice")
	for _, kw := range []string{"意图分类器", "AskRootCause、AskAdvice", "Unknown"} {
		if !strings.Contains(got, kw) {
			t.Fatalf("IntentClassifySystem 应含 %q: %s", kw, got)
		}
	}
}

func TestEvalJudgeUserRenders(t *testing.T) {
	resetRegistry(t)
	got := EvalJudgeUser("t-1", "EV", "RCA", "根因是否准确")
	for _, kw := range []string{"评测任务: t-1", "证据:EV", "RCA:RCA", "评分维度: 根因是否准确"} {
		if !strings.Contains(got, kw) {
			t.Fatalf("EvalJudgeUser 应含 %q: %s", kw, got)
		}
	}
}

func TestKBPromptsRender(t *testing.T) {
	resetRegistry(t)
	if got := KBSummarizePrompt("某设备处置手册"); !strings.Contains(got, "1-2句") || !strings.Contains(got, "某设备处置手册") {
		t.Fatalf("KBSummarizePrompt = %q", got)
	}
	if got := KBGenQuestionsPrompt("某设备处置手册"); !strings.Contains(got, "2-3个") || !strings.Contains(got, "某设备处置手册") {
		t.Fatalf("KBGenQuestionsPrompt = %q", got)
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

func TestIntentRewriteSplitSystemContainsSplitRule(t *testing.T) {
	resetRegistry(t)
	for _, kw := range []string{"问题理解模块", "sub_questions", "rewritten"} {
		if !strings.Contains(IntentRewriteSplitSystem(), kw) {
			t.Fatalf("IntentRewriteSplitSystem 应含 %q: %s", kw, IntentRewriteSplitSystem())
		}
	}
}

func TestRCAReactSystemContainsReActRule(t *testing.T) {
	resetRegistry(t)
	for _, kw := range []string{"write_scratchpad", "final_answer", "gaps"} {
		if !strings.Contains(RCAReactSystem(), kw) {
			t.Fatalf("RCAReactSystem 应含 %q: %s", kw, RCAReactSystem())
		}
	}
}

func TestEvalJudgeSystemContainsVerdictRule(t *testing.T) {
	resetRegistry(t)
	for _, kw := range []string{"INSUFFICIENT_INFO", "score", "verdict"} {
		if !strings.Contains(EvalJudgeSystem(), kw) {
			t.Fatalf("EvalJudgeSystem 应含 %q: %s", kw, EvalJudgeSystem())
		}
	}
}
