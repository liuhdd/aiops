package retrieve

import (
	"math"
	"testing"
	"time"

	"aiops/internal/model"
)

func mkCand(id string, bm25, vec, sig, rec float64) cand {
	return cand{Chunk: model.KbKnowledgeChunk{ChunkID: id}, BM25: bm25, Vec: vec, Signal: sig, Recency: rec}
}

func TestRankCasesOrderByFinal(t *testing.T) {
	now := time.Now()
	cands := []cand{
		mkCand("semantic", 0.0, 1.0, 0.0, 0.5),
		mkCand("labeled", 0.0, 0.0, 1.0, 0.5),
	}
	got := RankCases(cands, now)
	if got[0].Chunk.ChunkID != "semantic" {
		t.Fatalf("向量权重最高应排前, got %s first", got[0].Chunk.ChunkID)
	}
}

func TestRankManualsBM25Dominates(t *testing.T) {
	now := time.Now()
	cands := []cand{
		mkCand("kw", 1.0, 0.0, 0.0, 0.0),
		mkCand("vec", 0.0, 0.9, 0.0, 0.0),
	}
	got := RankManuals(cands, now)
	if got[0].Chunk.ChunkID != "kw" {
		t.Fatalf("手册 BM25 权重最高应排前, got %s first", got[0].Chunk.ChunkID)
	}
}

func TestRankManualsNoRecency(t *testing.T) {
	now := time.Now()
	// 时效与 BM25 相反：若时效参与融合，b 会因高 recency 反超 a；实际应仅由 BM25 定序。
	cands := []cand{mkCand("a", 1.0, 0, 0, 0.1), mkCand("b", 0.5, 0, 0, 0.9)}
	got := RankManuals(cands, now)
	if got[0].Chunk.ChunkID != "a" || got[0].Final <= got[1].Final {
		t.Fatalf("手册应仅由BM25定序且不受时效影响: a=%+v b=%+v", got[0], got[1])
	}
	if math.Abs(got[0].Final-0.5) > 1e-9 || math.Abs(got[1].Final) > 1e-9 {
		t.Fatalf("BM25 minmax 归一化后 a=0.5 b=0.0, got a=%f b=%f", got[0].Final, got[1].Final)
	}
}

func TestRankEmpty(t *testing.T) {
	got := RankCases(nil, time.Now())
	if len(got) != 0 {
		t.Fatalf("空输入应空输出, got %v", got)
	}
}
