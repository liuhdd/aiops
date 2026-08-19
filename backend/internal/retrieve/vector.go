package retrieve

import (
	"math"
	"strings"
	"time"

	"aiops/internal/model"
)

// Cosine 计算两向量余弦相似度；维度不一致返回 0。
func Cosine(a, b []float64) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	dot := 0.0
	for i := range a {
		dot += a[i] * b[i]
	}
	return dot
}

// Query 混合检索查询条件；空字段忽略。
type Query struct {
	Text     string
	Category model.KBCategory
	Metric   string
	Device   string
	Vendor   string
	Model    string
	Intents  []string
}

// LabelScore 案例信号：metric/device 标签命中 + keywords 词重叠 + 意图命中。
func LabelScore(kc model.KbKnowledgeChunk, q Query) float64 {
	score := 0.0
	if q.Metric != "" && containsStr(kc.Metrics, q.Metric) {
		score += 1
	}
	if q.Device != "" && containsStr(kc.DeviceTypes, q.Device) {
		score += 1
	}
	if q.Text != "" {
		for _, t := range Tokenize(q.Text) {
			if len(t) > 1 && containsStr(kc.Keywords, t) {
				score += 0.5
			}
		}
	}
	if len(q.Intents) > 0 && intersect(q.Intents, kc.Intents) {
		score += 1
	}
	return score
}

// FieldScore 手册信号：vendor/model/device_types/metrics 命中；
// query 携带字段但全部不命中时返回 0.1 强降权。
func FieldScore(kc model.KbKnowledgeChunk, q Query) float64 {
	hits := 0.0
	if q.Vendor != "" && containsStr(kc.Vendor, q.Vendor) {
		hits++
	}
	if q.Model != "" && containsStr(kc.Model, q.Model) {
		hits++
	}
	if q.Device != "" && containsStr(kc.DeviceTypes, q.Device) {
		hits++
	}
	if q.Metric != "" && containsStr(kc.Metrics, q.Metric) {
		hits++
	}
	if hits == 0 && (q.Vendor != "" || q.Model != "" || q.Device != "" || q.Metric != "") {
		return 0.1
	}
	return hits
}

// RecencyScore 时效衰减：60 分钟半衰期。
func RecencyScore(createdAt time.Time, now time.Time) float64 {
	ageMin := now.Sub(createdAt).Minutes()
	if ageMin < 0 {
		ageMin = 0
	}
	return math.Exp(-ageMin / 60)
}

func containsStr(list []string, s string) bool {
	for _, v := range list {
		if strings.EqualFold(v, s) {
			return true
		}
	}
	return false
}

func intersect(a, b []string) bool {
	for _, x := range a {
		for _, y := range b {
			if x == y {
				return true
			}
		}
	}
	return false
}
