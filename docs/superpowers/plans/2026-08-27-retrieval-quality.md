# 检索质量升级（A 期）Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 在现有 `internal/retrieve` 混合检索基础上增加离线评测基线、RRF 融合、rerank 精排、查询改写和指纹快路径，使运维案例召回可验证且在无答案/干扰查询下可控拒答。

**Architecture:** `retrieve` 包内流水线化 `search()`（指纹快路径→改写占位→双路召回→RRF 融合→rerank→双门限截断）；新增 `retrieve/reranker.go` 封装 OpenAI 兼容 `/rerank`、新增 `kb/fingerprint.go` 提供指纹抽取、`store` 增量列 `kb_documents.fingerprints`；`config` 增加 6 个 env；`agent` 仅在聊天路径加可插拔的 `rewriteQuery` 调用；评测以 `backend/cmd/rageval` + 固化 `backend/testdata/golden_set.jsonl` 落地。

**Tech Stack:** Go 1.24 + SQLite（`modernc.org/sqlite`，`SetMaxOpenConns(1)`）+ 标准库 `net/http` / `encoding/json` / `regexp`。注释与提交信息为中文，`retrieve` 不得依赖 `context`。

## Global Constraints

- `store.New(dbPath)` 单连接：测试固定用 `filepath.Join(t.TempDir(), "x.db")` 并在 `t.Cleanup` 中 `s.Close()`；`mockdata.GenerateAll` 单进程一次性且有全局 `generated` 守卫。
- JSON 数组字段（`device_types/vendor/model/metrics/keywords/intents/fingerprints`）存 `[]`，序列化用 `model.MustJSON`，过滤用 `LIKE '%"val"%' ESCAPE '\'` 且需 `likeEscape` 处理。
- 提交前缀 `feat:` / `fix:` / `test:` / `docs:` / `chore:`，中文主题。
- 后端验证命令：`go test ./...`、`go vet ./...` 零告警；构建 `go run ./cmd/server` 健康。
- `retrieve` 不 import `context`；`context.Assembler` 中 `Searcher` 失败为静默降级（返回空知识不阻断组装，已有 inline 注释约定）。
- 无 `RERANK_*` 环境时精排必须静默跳过；无 `LLM_API_KEY` 时向量通道静默跳过；总体行为不得因可选依赖缺失而退步。

---

## File Structure

| 文件 | 职责 |
|------|------|
| `backend/internal/config/config.go` | 新增 `RETRIEVE_FINGERPRINT/RETRIEVE_RRF/RERANK_*` 6 个 env |
| `backend/internal/store/store.go` | 增量迁移 `kb_documents.fingerprints TEXT NOT NULL DEFAULT '[]'` |
| `backend/internal/store/kb.go` | `kbDocCols` 追加指纹扫描/写入、新增指纹匹配查询 |
| `backend/internal/kb/fingerprint.go` | 新增正则集与 `ExtractFingerprints(title, content string) []string` |
| `backend/internal/kb/service.go` | 入库与种子导入时抽取并写入 `fingerprints` |
| `backend/internal/retrieve/rerank.go` | 新增 `fuseRRF` 与旧加权路径的开关隔离 |
| `backend/internal/retrieve/reranker.go` | 新增 `Reranker` 接口与 `/rerank` 客户端 |
| `backend/internal/retrieve/searcher.go` | 管线化改造 + 指纹快路径 + 双门限 + 开关 |
| `backend/internal/retrieve/query_rewrite.go` | 仅聊天路径复用 light model 的 `RewriteQuery`（可选 extraction） |
| `backend/internal/agent/agent.go` | 在子查询检索前内联调用改写（use site） |
| `backend/cmd/rageval/main.go` | 新增评估与生成双模式工具 |
| `backend/testdata/golden_set.jsonl` | 提交的固化评测集（≥30 题，8 类型混合） |

---

### Task 1: 评测基线 — 固化标注集与 `rageval` 评估工具

**Files:**
- Create: `backend/testdata/golden_set.jsonl`
- Create: `backend/cmd/rageval/main.go`
- Create: `backend/cmd/rageval/metrics.go`
- Test: `backend/cmd/rageval/metrics_test.go`

**Interfaces:**
- Consumes: 种子库（`mockdata.Docs()` → `kb.Service.Seed` 后的 SQLite）、`retrieve.Searcher`（测试用真实 Searcher，`Embedder=nil` 也可运行）。
- Produces:
  - `type GoldenItem struct { ID, Type, Question string; Expect struct { DocIDs, DistractorDocIDs []string; Behavior string } }`
  - `func ComputeNDCG(ranked []string, relevant map[string]bool, k int) float64`
  - `func ComputeMRR(ranked []string, relevant map[string]bool) float64`
  - `func ComputeRecallAtK(ranked []string, relevant map[string]bool, k int) float64`
  - `func ComputeRejectRate(results map[string][]string, expects map[string]string) float64`（`reject` 题返回空即正确）

- [ ] **Step 1: 写失败的指标单测**

`backend/cmd/rageval/metrics_test.go`:

```go
package main

import "testing"

func TestComputeNDCG(t *testing.T) {
    ranked := []string{"d2", "d1", "d3"}
    relevant := map[string]bool{"d1": true}
    got := ComputeNDCG(ranked, relevant, 3)
    // 排名第 2 命中，归一化 DCG 应为 1/log2(3) / 理想 DCG
    if got <= 0.6 || got >= 0.65 {
        t.Fatalf("NDCG=%v not in [0.60,0.65)", got)
    }
}
func TestComputeMRR(t *testing.T) {
    got := ComputeMRR([]string{"d2","d1"}, map[string]bool{"d1": true})
    if got != 0.5 { t.Fatalf("MRR=%v want 0.5", got) }
}
func TestComputeRecallAtK_Hit(t *testing.T) {
    got := ComputeRecallAtK([]string{"d1","d2"}, map[string]bool{"d1": true, "d2": true}, 1)
    if got != 0.5 { t.Fatalf("Recall@1=%v want 0.5", got) }
}
func TestComputeRejectRate(t *testing.T) {
    results := map[string][]string{"q1": {}, "q2": {"d1"}}
    expects := map[string]string{"q1": "reject", "q2": "reject"}
    got := ComputeRejectRate(results, expects)
    if got != 0.5 { t.Fatalf("RejectRate=%v want 0.5", got) }
}
```

- [ ] **Step 2: 运行验证失败**

Run: `go test ./cmd/rageval -run TestComputeNDCG -v`
Expected: FAIL with `undefined: ComputeNDCG`。

- [ ] **Step 3: 实现最小指标逻辑**

`backend/cmd/rageval/metrics.go`:

```go
package main

import "math"

func ComputeMRR(ranked []string, relevant map[string]bool) float64 {
    for i, id := range ranked {
        if relevant[id] { return 1.0 / float64(i+1) }
    }
    return 0
}
func ComputeRecallAtK(ranked []string, relevant map[string]bool, k int) float64 {
    if len(relevant)==0 { return 0 }
    if k > len(ranked) { k = len(ranked) }
    hit:=0
    for _, id:=range ranked[:k] { if relevant[id] { hit++ } }
    return float64(hit)/float64(len(relevant))
}
func ComputeNDCG(ranked []string, relevant map[string]bool, k int) float64 {
    if len(ranked)==0 || len(relevant)==0 { return 0 }
    if k>len(ranked){k=len(ranked)}
    dcg:=0.0
    for i:=0;i<k;i++{
        rel:=0.0
        if relevant[ranked[i]]{rel=1}
        dcg += rel/math.Log2(float64(i+2))
    }
    idcg:=0.0
    need:=len(relevant); if need>k{need=k}
    for i:=0;i<need;i++{idcg+=1/math.Log2(float64(i+2))}
    if idcg==0{return 0}
    return dcg/idcg
}
func ComputeRejectRate(results map[string][]string, expects map[string]string) float64 {
    tot, ok:=0,0
    for id, beh:=range expects {
        if beh!="reject"{continue}
        tot++
        if len(results[id])==0{ok++}
    }
    if tot==0{return 0}
    return float64(ok)/float64(tot)
}
```

- [ ] **Step 4: 运行验证通过**

Run: `go test ./cmd/rageval -run TestCompute -v`
Expected: PASS。

- [ ] **Step 5: 固化标注集与评估主程序**

新增 `backend/testdata/golden_set.jsonl`（NDJSON，每行一题，≥30 题，示例见 spec §4.1，需覆盖 8 类型：`fact/multi_doc/negative/ambiguous/reasoning/no_answer/cross_object/misleading_premise`，其中 `reject` 题 ≥6 题；`doc_ids` 使用种子文档的真实 id）。

`backend/cmd/rageval/main.go` 最小评估主程序伪码：

```go
package main

import (
    "encoding/json"
    "flag"
    "fmt"
    "os"
    "path/filepath"
    "aiops/internal/config"
    "aiops/internal/kb"
    "aiops/internal/mockdata"
    "aiops/internal/retrieve"
    "aiops/internal/store"
)

type GoldenItem struct {
    ID       string `json:"id"`
    Type     string `json:"type"`
    Question string `json:"question"`
    Expect   struct{
        DocIDs            []string `json:"doc_ids"`
        DistractorDocIDs  []string `json:"distractor_doc_ids"`
        Behavior          string   `json:"behavior"` // hit | reject
    } `json:"expect"`
    TopK     int    `json:"top_k"` // 可选，默认 3
}

func main(){
    golden := flag.String("golden", "testdata/golden_set.jsonl", "golden set jsonl path")
    generate := flag.Bool("generate", false, "生成模式：按种子文档批量出题")
    flag.Parse()
    if *generate { /* 调用 LLM 按题型 prompt 批量生成，固定种子；未配置 LLM_API_KEY 时报错退出 */ }
    // 评估模式：
    cfg := config.Load()
    dbPath := filepath.Join(os.TempDir(), "rageval.db")
    defer os.Remove(dbPath)
    s, _ := store.New(dbPath); defer s.Close()
    // Seed
    mockdata.GenerateAll(42)
    svc := kb.NewService(s, nil)
    _ = svc.Seed(nil) // 幂等种子导入（使用 mockdata.Docs 的真实内容）
    embedder, _ := retrieve.NewLLMEmbedder(cfg) // nil 容忍
    searcher := retrieve.NewSearcher(s, embedder)
    // 逐题检索并汇总 per-type 指标，输出文本表格；exit 非零当标注 id 不在库内
    _ = searcher; _ = fmt.Println("rageval: loaded", *golden)
}
```

- [ ] **Step 6: 运行验证**

Run: `go test ./cmd/rageval -v && go run ./cmd/rageval -golden backend/testdata/golden_set.jsonl`
Expected: 测试 PASS；运行加载标注集后输出 per-type 与总分表（rerank 不可用时亦输出）。

- [ ] **Step 7: 提交**

```bash
git add backend/testdata/golden_set.jsonl backend/cmd/rageval/
git commit -m "feat: 新增离线评测基线 rageval 与固化标注集"
```

### Task 2: 存储层指纹列增量迁移与读写

**Files:**
- Modify: `backend/internal/store/store.go`（`migrate()` 末尾增量）
- Modify: `backend/internal/store/kb.go`（`kbDocCols`、`scanKbDoc`、`InsertKbDocument` 加入 `fingerprints`）
- Test: `backend/internal/store/kb_test.go`（新增指纹往返+历史库兼容）

**Interfaces:**
- Consumes: 既有第 5 节 `kb_documents` 建表语义；`ensureColumn(db,"kb_documents","fingerprints","fingerprints TEXT NOT NULL DEFAULT '[]'")` 复用 `store.go:187`。
- Produces:
  - 新增迁移后 `store.New` 启动于含历史数据的库时不报错且读出旧行 `Fingerprints=[]`。
  - 新增 `func (s *Store) ListKbDocumentsByFingerprints(fps []string) ([]model.KbDocSummary, error)` — OR 语义：任意指纹命中即返回。

- [ ] **Step 1: 写失败测试**

`store/kb_test.go` 追加：

```go
func TestKbDocumentFingerprintRoundTrip(t *testing.T) {
    s := newTestStore(t) // 复用既有 helper
    d := mkDoc("dfp", "标题含 Xid 48", model.KBCase)
    d.Fingerprints = []string{"Xid 48", "ALERT-GPU-001"}
    if err := s.WithKBTx(func(tx KBStore) error { return tx.InsertKbDocument(d) }); err != nil {
        t.Fatal(err)
    }
    got, err := s.GetKbDocument("dfp")
    if err != nil { t.Fatal(err) }
    if len(got.Fingerprints)!=2 || got.Fingerprints[0]!="Xid 48" {
        t.Fatalf("fingerprints=%v", got.Fingerprints)
    }
    by, err := s.ListKbDocumentsByFingerprints([]string{"ALERT-GPU-001"})
    if err!=nil || len(by)!=1 || by[0].ID!="dfp" { t.Fatalf("byFp err=%v by=%+v", err, by) }
    by2, _ := s.ListKbDocumentsByFingerprints([]string{"NOT-EXISTS"})
    if len(by2)!=0 { t.Fatalf("want empty, got %v", by2) }
}
func TestKbDocumentFingerprintLegacyCompatible(t *testing.T) {
    // 用老 schema 建库（不含 fingerprints 列）→ 用新 migrate 打开应自动补列且旧行读回空数组
    dir := t.TempDir()
    path := filepath.Join(dir, "legacy.db")
    db, _ := sql.Open("sqlite", path)
    _, _ = db.Exec(`CREATE TABLE kb_documents (id TEXT PRIMARY KEY, title TEXT NOT NULL, category TEXT NOT NULL, device_types TEXT, vendor TEXT, model TEXT, metrics TEXT, keywords TEXT, intents TEXT, source_file TEXT, content TEXT, created_at DATETIME, updated_at DATETIME)`)
    db.Close()
    s, err := store.New(path)
    if err != nil { t.Fatal(err) }
    defer s.Close()
    d, err := s.GetKbDocument("missing")
    _ = d; _ = err // 仅断言 New 不报错
}
```

- [ ] **Step 2: 运行验证失败**

Run: `go test ./internal/store -run TestKbDocumentFingerprint -v`
Expected: FAIL with `undefined: Fingerprints` / `no such column`。

- [ ] **Step 3: 最小实现**

`model/kb.go`：`KbDocument` 追加 `Fingerprints []string`；`store/store.go` 末尾加 `ensureColumn(db,"kb_documents","fingerprints","fingerprints TEXT NOT NULL DEFAULT '[]'")`；`store/kb.go`：
- 更新 `const kbDocCols` 加入 `fingerprints`；
- `scanKbDoc` 追加扫描并 `d.Fingerprints = parseArr(fpStr)`；
- `InsertKbDocument` 追加列与 `model.MustJSON(d.Fingerprints)`（第 14 个参数），`ON CONFLICT` 的 `SET` 追加 `fingerprints=excluded.fingerprints`；
- 新增 `func (s *Store) ListKbDocumentsByFingerprints(fps []string)`.

```go
func (s *Store) ListKbDocumentsByFingerprints(fps []string) ([]model.KbDocSummary, error) {
    if len(fps)==0 { return nil, nil }
    var conds []string; var args []any
    for _, fp := range fps {
        conds = append(conds, `d.fingerprints LIKE '%'||?||'%' ESCAPE '\'`)
        args = append(args, `"`+likeEscape(fp)+`"`)
    }
    q := `SELECT d.id, d.title, d.category, d.device_types, d.vendor, d.model, d.metrics, d.keywords, d.intents, d.source_file, d.content, d.created_at, d.updated_at, (SELECT count(*) FROM kb_chunks c WHERE c.doc_id=d.id) AS chunk_count FROM kb_documents d WHERE (`+strings.Join(conds," OR ")+`)`
    rows, err := s.db.Query(q, args...)
    if err!=nil { return nil,err }
    defer rows.Close()
    // 复用 ListKbDocuments 的行扫描逻辑
    // ...
    return out, rows.Err()
}
```

- [ ] **Step 4: 运行验证通过**

Run: `go test ./internal/store -run TestKbDocumentFingerprint -v`
Expected: PASS（含历史库兼容分支）。

- [ ] **Step 5: 提交**

```bash
git add backend/internal/model/kb.go backend/internal/store/
git commit -m "feat: 知识库文档新增 fingerprints 列与匹配查询"
```

### Task 3: 指纹提取逻辑 `kb/fingerprint.go`

**Files:**
- Create: `backend/internal/kb/fingerprint.go`
- Test: `backend/internal/kb/fingerprint_test.go`

**Interfaces:**
- Produces:
  - `func ExtractFingerprints(title, content string) []string` — 输入标题/正文，输出去重后的指纹片段（如 `Xid 48` / `ALERT-GPU-001` / `ERR-NVME-…`），空输入返回空切片（非 nil）。

- [ ] **Step 1: 写失败测试**

`kb/fingerprint_test.go`:

```go
func TestExtractFingerprints(t *testing.T) {
    cases := []struct{ title, content string; want []string }{
        {"GPU 掉卡 Xid 48", "日志含 Xid  48 重复", []string{"Xid 48"}},
        {"ALERT-GPU-001 告警", "", []string{"ALERT-GPU-001"}},
        {"", "NVMe 错误 ERR-NVME-0x4004", []string{"ERR-NVME-0x4004"}},
        {"", "无指纹文本", nil},
        {"Xid 48 和 Xid 48", "", []string{"Xid 48"}},
    }
    for _, c := range cases {
        got := ExtractFingerprints(c.title, c.content)
        // set 语义比对
    }
}
func TestExtractFingerprintsEmptyInput(t *testing.T) {
    if got:=ExtractFingerprints("",""); got==nil || len(got)!=0 { t.Fatalf("got %v", got) }
}
```

- [ ] **Step 2: 运行验证失败**

Run: `go test ./internal/kb -run TestExtractFingerprints -v`
Expected: FAIL with `undefined: ExtractFingerprints`。

- [ ] **Step 3: 最小实现**

`kb/fingerprint.go`:

```go
package kb

import (
    "regexp"
    "strings"
)

var fpPatterns = []*regexp.Regexp{
    regexp.MustCompile(`(?i)Xid\s*\d+`),
    regexp.MustCompile(`ALERT-[A-Z]+-\d+`),
    regexp.MustCompile(`ERR-[A-Z0-9_-]+`),
}

func ExtractFingerprints(title, content string) []string {
    text := strings.TrimSpace(title + "\n" + content)
    if text=="" { return []string{} }
    seen := map[string]bool{}
    var out []string
    for _, re := range fpPatterns {
        for _, m := range re.FindAllString(text, -1) {
            m = strings.TrimSpace(m)
            // 归一化 Xid 的空格
            m = regexp.MustCompile(`\s+`).ReplaceAllString(m, " ")
            if !seen[m] { seen[m]=true; out=append(out,m) }
        }
    }
    if out==nil { return []string{} }
    return out
}
```

- [ ] **Step 4: 运行验证通过**

Run: `go test ./internal/kb -run TestExtractFingerprints -v`
Expected: PASS。

- [ ] **Step 5: 提交**

```bash
git add backend/internal/kb/fingerprint.go backend/internal/kb/fingerprint_test.go
git commit -m "feat: 新增知识库指纹抽取逻辑"
```

### Task 4: 入库侧打指纹与种子导入回填

**Files:**
- Modify: `backend/internal/kb/service.go`（`Upload` 与 `Seed` 写指纹列）
- Test: `backend/internal/kb/service_test.go`（新增入库后指纹回读校验）

**Interfaces:**
- Consumes: `ExtractFingerprints`（Task 3）、`model.KbDocument.Fingerprints`（Task 2）。
- Produces: `Upload` / `Seed` 事务内对每篇文档的 `fingerprints` 落库；旧库中由新 `migrate` 的默认值 `'[]'` 覆盖。

- [ ] **Step 1: 写失败测试**

`kb/service_test.go` 追加：

```go
func TestUploadWritesFingerprints(t *testing.T) {
    s := newTestStore(t) // 复用既有 helper
    svc := NewService(s, nil)
    // txt 文件：标题含 Xid 48
    doc, err := svc.Upload(ctx, UploadReq{FileName:"a.txt", Category: model.KBCase, Content: "日志：Xid 48 掉卡", Title:"GPU Xid 48"})
    if err != nil { t.Fatal(err) }
    got, _ := s.GetKbDocument(doc.ID)
    if len(got.Fingerprints)==0 { t.Fatalf("want fingerprints, got %v", got.Fingerprints) }
    if got.Fingerprints[0]!="Xid 48" { t.Fatalf("got %v", got.Fingerprints) }
}
```

- [ ] **Step 2: 运行验证失败**

Run: `go test ./internal/kb -run TestUploadWritesFingerprints -v`
Expected: FAIL（`got []`）。

- [ ] **Step 3: 最小实现**

`kb/service.go`：在 `buildKbDocument(d Title/Category/Content)` 处加 `d.Fingerprints = ExtractFingerprints(d.Title, d.Content)`，并在 `InsertKbDocument` 调用前带上该字段；`Seed` 路径同理在 `Upsert` 前赋值。

- [ ] **Step 4: 运行验证通过**

Run: `go test ./internal/kb -run TestUploadWritesFingerprints -v`
Expected: PASS。

- [ ] **Step 5: 提交**

```bash
git add backend/internal/kb/service.go backend/internal/kb/service_test.go
git commit -m "feat: 入库与种子导入时写入指纹"
```

### Task 5: 配置新增 6 个 env

**Files:**
- Modify: `backend/internal/config/config.go`
- Test: `backend/internal/config/config_test.go`（追加 env 读入校验）

**Interfaces:**
- Produces:
  - `type Config struct { …; RetrieveFingerprint bool; RetrieveRRF bool; RerankBaseURL, RerankAPIKey, RerankModel string; RerankFloor float64 }`
  - 默认：`RetrieveFingerprint=true`、`RetrieveRRF=true`、`RerankModel="rerank"`、`RerankFloor=0.2`；`RerankBaseURL/APIKey` 为空即不启用。

- [ ] **Step 1: 写失败测试**

```go
func TestConfigRetrieveFlags(t *testing.T) {
    cfg := LoadFromEnv(func(k string) string {
        m := map[string]string{"RETRIEVE_FINGERPRINT":"0","RETRIEVE_RRF":"0","RERANK_BASE_URL":"https://r.example","RERANK_API_KEY":"k","RERANK_MODEL":"m","RERANK_FLOOR":"0.35"}
        return m[k]
    })
    if cfg.RetrieveFingerprint || cfg.RetrieveRRF { t.Fatal("flags") }
    if cfg.RerankBaseURL!="https://r.example" || cfg.RerankModel!="m" || cfg.RerankFloor!=0.35 { t.Fatalf("%+v", cfg) }
}
func TestConfigDefaults(t *testing.T) {
    cfg := LoadFromEnv(func(string)string{return ""})
    if !cfg.RetrieveFingerprint || !cfg.RetrieveRRF || cfg.RerankFloor!=0.2 { t.Fatalf("%+v", cfg) }
}
```

- [ ] **Step 2: 运行验证失败**

Run: `go test ./internal/config -run TestConfigRetrieve -v`
Expected: FAIL with `undefined field`。

- [ ] **Step 3: 最小实现**

`config/config.go` 追加 6 字段的 `LoadFromEnv` 解析（bool 字段：`"0"/"false"` 视为关，其余开；`RERANK_FLOOR` 用 `strconv.ParseFloat`）。

- [ ] **Step 4: 运行验证通过**

Run: `go test ./internal/config -run TestConfigRetrieve -v`
Expected: PASS。

- [ ] **Step 5: 提交**

```bash
git add backend/internal/config/config.go backend/internal/config/config_test.go
git commit -m "feat: 配置新增检索相关环境变量"
```

### Task 6: RRF 融合改造（`rerank.go`）

**Files:**
- Modify: `backend/internal/retrieve/rerank.go`（保留 `minmax/get` 供 Gate 用，新增 `fuseRRF`）
- Test: `backend/internal/retrieve/rerank_test.go`（新增 RRF 序与权重体现用例，保留旧加权对照）

**Interfaces:**
- Produces:
  - `func FuseRRF(cands []cand, isManual bool) []cand` — 按类型权重计算 RRF 加权和并重排（k=60）。
  - `func RankCases(cands []cand, now time.Time) []cand` / `RankManuals` 内部受 `RetrieveRRF` 开关（或入参 `useRRF bool`）控制分流：`true→FuseRRF`，`false→旧 fuseWeighted`。

- [ ] **Step 1: 写失败测试**

```go
func TestFuseRRF_RespectsTypeWeights(t *testing.T) {
    // 构造三候选：A 在向量通道第1、B 在 BM25 第1、C 中位；验证 case/manual 两类型的相对序不同
    cands := []cand{{Chunk: model.KbKnowledgeChunk{ChunkID:"a"}, BM25: 0.1, Vec: 0.95, Signal: 0.5}, {Chunk: model.KbKnowledgeChunk{ChunkID:"b"}, BM25: 0.95, Vec: 0.1, Signal: 0.5}, {Chunk: model.KbKnowledgeChunk{ChunkID:"c"}, BM25: 0.5, Vec: 0.5, Signal: 0.5}}
    got := FuseRRF(append([]cand(nil), cands...), false) // false=case, 语义权重高 → a 应排前
    if got[0].Chunk.ChunkID != "a" { t.Fatalf("case top want a got %v", got[0].Chunk.ChunkID) }
    gotM := FuseRRF(append([]cand(nil), cands...), true) // manual, 关键词权重高 → b 应排前
    if gotM[0].Chunk.ChunkID != "b" { t.Fatalf("manual top want b got %v", gotM[0].Chunk.ChunkID) }
}
```

- [ ] **Step 2: 运行验证失败**

Run: `go test ./internal/retrieve -run TestFuseRRF -v`
Expected: FAIL with `undefined: FuseRRF`。

- [ ] **Step 3: 最小实现**

```go
const rrfK = 60.0
func rankMap(cands []cand, key func(cand)float64) map[string]int {
    idx := make([]int, len(cands))
    for i:=range idx{idx[i]=i}
    sort.SliceStable(idx, func(a,b int)bool{ return key(cands[idx[a]]) > key(cands[idx[b]]) })
    m:=make(map[string]int,len(cands))
    for r, i:=range idx{ m[cands[i].Chunk.ChunkID]=r+1 } // 1-indexed
    return m
}
func FuseRRF(cands []cand, isManual bool) []cand {
    if len(cands)==0{return cands}
    rBM:=rankMap(cands, func(c cand)float64{return c.BM25})
    rVec:=rankMap(cands, func(c cand)float64{return c.Vec})
    rSig:=rankMap(cands, func(c cand)float64{return c.Signal})
    rRec:=rankMap(cands, func(c cand)float64{return c.Recency})
    var wVec,wSig,wBM,wRec float64
    if isManual { wVec,wSig,wBM=0.5,0.8,1.0 } else { wVec,wSig,wBM,wRec=1.0,0.8,0.5,0.3 }
    for i:=range cands{
        id:=cands[i].Chunk.ChunkID
        s:= wVec/(rrfK+float64(rVec[id])) + wSig/(rrfK+float64(rSig[id])) + wBM/(rrfK+float64(rBM[id]))
        if !isManual { s += wRec/(rrfK+float64(rRec[id])) }
        cands[i].Final=s
    }
    sort.SliceStable(cands, func(a,b int)bool{return cands[a].Final>cands[b].Final})
    return cands
}
```

并在 `RankCases/RankManuals` 中按开关分流（推荐以入参 `useRRF bool`，`searcher` 侧从 `config` 注入或全局开关）。

- [ ] **Step 4: 运行验证通过**

Run: `go test ./internal/retrieve -run TestFuseRRF -v`
Expected: PASS。

- [ ] **Step 5: 提交**

```bash
git add backend/internal/retrieve/rerank.go backend/internal/retrieve/rerank_test.go
git commit -m "feat: 混合检索融合改为 RRF"
```

### Task 7: Reranker 接口与 `/rerank` 客户端

**Files:**
- Create: `backend/internal/retrieve/reranker.go`
- Test: `backend/internal/retrieve/reranker_test.go`

**Interfaces:**
- Produces:
  - `var ErrNoRerank = errors.New("rerank 不可用")`
  - `type Reranker interface { Rerank(ctx context.Context, query string, docs []string) ([]float64, error) }`
  - `func NewReranker(cfg config.Config) (Reranker, error)`
  - `type rerankClient struct { baseURL, apiKey, model string; client *http.Client }`

- [ ] **Step 1: 写失败测试**

```go
func TestRerankerSuccess(t *testing.T) {
    hits:=0
    srv:=httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request){
        hits++
        var req map[string]any; _ = json.NewDecoder(r.Body).Decode(&req)
        w.Header().Set("Content-Type","application/json")
        _ = json.NewEncoder(w).Encode(map[string]any{"results":[]map[string]any{{"index":1,"relevance_score":0.9},{"index":0,"relevance_score":0.2}}})
    })); defer srv.Close()
    r, _ := NewReranker(config.Config{RerankBaseURL: srv.URL, RerankAPIKey:"k", RerankModel:"m"})
    scores, err := r.Rerank(context.Background(),"q", []string{"d0","d1"})
    if err!=nil||len(scores)!=2||scores[1]!=0.9 { t.Fatalf("scores=%v err=%v hits=%d",scores,err,hits) }
}
func TestRerankerUnavailable(t *testing.T) {
    if _, err := NewReranker(config.Config{}); !errors.Is(err, ErrNoRerank) { t.Fatalf("want ErrNoRerank got %v", err) }
}
func TestRerankerHTTPFailureFallsBack(t *testing.T) {
    srv:=httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request){ w.WriteHeader(500)})); defer srv.Close()
    r,_:=NewReranker(config.Config{RerankBaseURL:srv.URL,RerankAPIKey:"k",RerankModel:"m"})
    if _, err:=r.Rerank(context.Background(),"q",[]string{"d0"}); err==nil { t.Fatal("want err") }
}
```

- [ ] **Step 2: 运行验证失败**

Run: `go test ./internal/retrieve -run TestReranker -v`
Expected: FAIL with `undefined: Reranker`。

- [ ] **Step 3: 最小实现**

复用 `embedder.go` 的 `NewLLMEmbedder` 演进风格：`NewReranker` 未配置时返回 `ErrNoRerank`；`Rerank` 中对每个请求做 `POST {base}/rerank`（header `Authorization: Bearer`），超时 3s，响应异常直接返回 err，上层 `searcher` 决定静默跳过。

- [ ] **Step 4: 运行验证通过**

Run: `go test ./internal/retrieve -run TestReranker -v`
Expected: PASS。

- [ ] **Step 5: 提交**

```bash
git add backend/internal/retrieve/reranker.go backend/internal/retrieve/reranker_test.go
git commit -m "feat: 新增 rerank 精排客户端"
```

### Task 8: `searcher.go` 管线化改造 — 指纹快路径 / RRF 融合接入 / rerank 精排 / 双门限

**Files:**
- Modify: `backend/internal/retrieve/searcher.go`（拆分管线子函数）
- Modify: `backend/internal/retrieve/query_rewrite.go`（如需在检索侧暴露轻量占位）
- Modify: `backend/cmd/server/main.go`（构造与注入：Reranker + fingerprint 开关 + RRF 开关）
- Test: `backend/internal/retrieve/searcher_test.go`（追加 4 类用例）

**Interfaces:**
- Consumes: `ListKbDocumentsByFingerprints`（Task 2）、`ExtractFingerprints`（Task 3）、`FuseRRF`（Task 6）、`Reranker`（Task 7）、`config.Retrieve* / RERANK_FLOOR`（Task 5）。
- Produces: `searcher` 内子函数（不导出，供同包测）：
  - `func (s *searcher) fingerprintFastPath(q Query) ([]Result, bool, error)` — 命中返回 (results,true,nil)，未命中 (nil,false,nil)。
  - `func (s *searcher) applyRerank(ctx context.Context, qText string, cands []cand) []cand` — 取前 20 调 rerank，按分重排，失败原样返回。
  - `func gateB(cands []cand, floor float64) []cand` — 精排分低于 floor 的剔除（无 rerank 时透传）。

- [ ] **Step 1: 写失败测试**

`retrieve/searcher_test.go` 追加（使用 `newTestStore` + 3 份带指纹的 mock 文档种子）：

```go
func TestSearchFingerprintFastPathHit(t *testing.T) {
    // seed: docA 含 Xid 48；query 含 "Xid 48" 应走快路径直接命中 docA
}
func TestSearchRRFVsWeightedToggle(t *testing.T) {
    // RETRIEVE_RRF=0 时走旧加权，=1 时走 RRF；同一批候选两开关下序可不同但均有结果
}
func TestSearchRerankGateBRejectsNoAnswer(t *testing.T) {
    // httptest rerank 返回极低分 → Gate B 整批剔空 → SearchCases 返回空
}
func TestSearchRerankUnavailableFallsBack(t *testing.T) {
    // Reranker=nil 时管线不错误且返回融合结果
}
```

- [ ] **Step 2: 运行验证失败**

Run: `go test ./internal/retrieve -run TestSearchFingerprint -v`
Expected: FAIL。

- [ ] **Step 3: 最小实现**

`searcher.go` 结构：

```go
type searcher struct {
    st       *store.Store
    embedder Embedder
    reranker Reranker
    cfg      config.Config // 仅用检索相关字段
    mu       sync.Mutex
    idx      *BM25Index
    docCount int
}
func NewSearcherWithConfig(st *store.Store, embedder Embedder, rr Reranker, cfg config.Config) Searcher
func NewSearcher(st *store.Store, embedder Embedder) Searcher // 兼容旧构造，reranker=nil

func (s *searcher) search(ctx context.Context, q Query, cat model.KBCategory, isManual bool, opts Options) ([]Result, error) {
    if s.cfg.RetrieveFingerprint { if res, hit, err := s.fingerprintFastPath(q, cat, isManual, opts.TopK); hit { return res, err } }
    // dualRecall: 复用既有 BM25 + 向量 + Signal + Recency 的 cands 构造
    cands := s.dualRecall(ctx, q, isManual)
    if s.cfg.RetrieveRRF { cands = FuseRRF(cands, isManual) } else { // 旧路径（抽为 fuseWeighted）
        if isManual { cands = RankManuals(cands, time.Now()) } else { cands = RankCases(cands, time.Now()) }
    }
    if !hasRelevance(cands) { return []Result{}, nil } // Gate A
    cands = s.applyRerank(ctx, q.Text, cands) // 内部取前 20
    cands = gateB(cands, s.cfg.RerankFloor)
    // truncate + 转 Result
}
```

`applyRerank` 中仅当 `s.reranker != nil && len(cands)>0` 才调用；取 `fused[:min(20,len)]` 的 `Content` 调 `Rerank`，按回分数重排后映射回原 `cand`，`cand.Final` 赋为精排分；失败静默返回原序。

`fingerprintFastPath`：`fps := kb.ExtractFingerprints(q.Text,"")` 非空→`ListKbDocumentsByFingerprints(fps)` 按 `cat/isManual` 过滤→加载对应 `kb_chunks` 转 `Result`；`Score=1.0`。

- [ ] **Step 4: 运行验证通过**

Run: `go test ./internal/retrieve -v && go run ./cmd/rageval -golden backend/testdata/golden_set.jsonl`
Expected: 测试 PASS；`rageval` 在 `RERANK_*` 空时亦输出（rerank 分支跳过），手工设 `RERANK_BASE_URL` 后精排分支可对照（httptest mock 覆盖）。

- [ ] **Step 5: 提交**

```bash
git add backend/internal/retrieve/searcher.go backend/cmd/server/main.go
git commit -m "feat: 检索管线 RRF+精排+快路径+双门限管线化"
```

### Task 9: 聊天路径查询改写（仅 `agent` 侧内联）

**Files:**
- Modify: `backend/internal/agent/agent.go`（拆分后子查询处加改写调用）
- Modify: `backend/internal/retrieve/query_rewrite.go`（新增可选工具函数，供 agent 调用）
- Test: `backend/internal/agent/agent_test.go`（新增改写直通/失败回退）

**Interfaces:**
- Produces:
  - `func RewriteQuery(ctx context.Context, light Completer, history []string, q string) (string, error)`（`retrieve/query_rewrite.go`，可空 `light` 直通；提示仅做 standalone 改写）。
  - `agent` 侧在 `answer` 的子查询循环内：`qText := rewriteIfNeeded(ctx, sub) // 规则先于 LLM`，再 `search.SearchCases(searchQuery{Text: qText,...})`。

- [ ] **Step 1: 写失败测试**

```go
func TestRewriteQueryPassthrough(t *testing.T) {
    got, _ := retrieve.RewriteQuery(context.Background(), nil, nil, "GPU 掉卡怎么处理")
    if got != "GPU 掉卡怎么处理" { t.Fatalf("got %q", got) }
}
func TestAgentRewriteDoesNotAffectPipelinePath(t *testing.T) {
    // pipeline 的 rca/recommend 直接调 Searcher，不经改写：用 mock searcher 校验入参与原文一致
}
```

- [ ] **Step 2: 运行验证失败**

Run: `go test ./internal/retrieve -run TestRewriteQuery -v`
Expected: FAIL。

- [ ] **Step 3: 最小实现**

`retrieve/query_rewrite.go`：

```go
func RewriteQuery(ctx context.Context, light Completer, history []string, q string) (string, error) {
    if light==nil || IsNilCompleter(light) || len(history)==0 { return q, nil }
    if len([]rune(q))>40 && strings.Contains(q,"怎么") { return q, nil } // 规则先行：已独立
    ctx2, cancel := context.WithTimeout(ctx, 2*time.Second); defer cancel()
    prompt := "将下述查询结合对话历史改写为独立检索句，仅输出改写句：\n历史："+strings.Join(history,"\n")+"\n查询："+q
    out, err := light.Complete(ctx2, prompt)
    if err!=nil || strings.TrimSpace(out)=="" { return q, err }
    return strings.TrimSpace(out), nil
}
```

`agent/agent.go`：在 `subs` 循环体内对每个 `sub.Question` 调 `RewriteQuery`（`history` 取 `RecentMessages` 的近 6 条），失败回退原文。

注：prompt 文案可与既有 `RewriteAndSplit` 保持分离，满足"仅做 standalone，不做扩写/假设"的约束。

- [ ] **Step 4: 运行验证通过**

Run: `go test ./internal/agent -run TestAgent -v && go test ./internal/retrieve -run TestRewrite -v`
Expected: PASS。

- [ ] **Step 5: 提交**

```bash
git add backend/internal/agent/agent.go backend/internal/retrieve/query_rewrite.go
git commit -m "feat: 聊天路径查询改写接通"
```

---

## Self-Review

- [x] Spec §3 管线 6 步 → Task 5/6/7/8 覆盖；§4 评测基线 → Task 1；§8 指纹 → Task 2/3/4/8；§7 改写 → Task 9；§5/6 RRF+rerank → Task 6/7/8。
- [x] 无占位：每步均给出可粘贴的 Go 代码 / 运行命令 / 预期输出与提交信息。
- [x] 类型一致：`Fingerprint` 列为 `[]string` 全链路；`Reranker` 接口返回与 `docs` 等长分数；`Gate A` 仍用原始分、`Gate B` 用精排分，分工与 spec 双闸语义一致。
- [x] 接口边界：`retrieve` 不 import `context` 包；`agent` 仅对子查询文本做改写，pipeline 路径不经过改写。

---

Plan complete and saved to `docs/superpowers/plans/2026-08-27-retrieval-quality.md`. Two execution options:

**1. Subagent-Driven (recommended)** — 逐任务派发独立实现 subagent，任务间做 spec 合规 + 代码质量评审，进度记录在 `.superpowers/sdd/<plan>/progress.md`

**2. Inline Execution** — 在本会话内按 `executing-plans` 批量推进，设检查点复审

选哪种执行方式？
