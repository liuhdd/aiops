package mockdata

import (
	"testing"
	"time"
)

func TestQueryMetricAnomaly(t *testing.T) {
	GenerateAll(42)
	now := time.Now()
	pts, err := QueryMetric("compute-01", "cpu_usage", now.Add(-2*time.Hour), now)
	if err != nil {
		t.Fatalf("QueryMetric: %v", err)
	}
	if len(pts) == 0 {
		t.Fatalf("no points")
	}
	last := pts[len(pts)-1]
	if last.Value < 80 {
		t.Fatalf("last cpu value = %v, want >= 80", last.Value)
	}
}

func TestSearchLogsKeyword(t *testing.T) {
	GenerateAll(42)
	now := time.Now()
	logs, err := SearchLogs("compute-02", []string{"Xid"}, now.Add(-2*time.Hour), now)
	if err != nil {
		t.Fatalf("SearchLogs: %v", err)
	}
	if len(logs) == 0 {
		t.Fatalf("no Xid logs found for compute-02")
	}
}

func TestPointCount(t *testing.T) {
	GenerateAll(7)
	now := time.Now().Truncate(time.Minute)
	pts, _ := QueryMetric("db-01", "disk_usage", now.Add(-2*time.Hour), now)
	if len(pts) != 25 {
		t.Fatalf("len(points) = %d, want 25", len(pts))
	}
}

func TestNewAnomalyScenarios(t *testing.T) {
	cases := []struct {
		dev, metric string
		min         float64
	}{
		{"db-01", "cpu_usage", 85},
		{"compute-01", "mem_usage", 90},
		{"web-01", "disk_usage", 95},
		{"storage-01", "disk_usage", 95},
	}
	for _, c := range cases {
		if got := scenario(c.dev, c.metric, 0); got < c.min {
			t.Fatalf("%s/%s = %v, want >= %v", c.dev, c.metric, got, c.min)
		}
	}
}

func TestNewAnomalyLogs(t *testing.T) {
	GenerateAll(42)
	now := time.Now()
	checks := []struct {
		dev string
		kws []string
	}{
		{"db-01", []string{"load", "cpu"}},
		{"compute-01", []string{"oom", "OutOfMemory", "memory"}},
		{"web-01", []string{"no space", "error"}},
		{"storage-01", []string{"no space", "error"}},
	}
	for _, c := range checks {
		logs, err := SearchLogs(c.dev, c.kws, now.Add(-3*time.Hour), now)
		if err != nil || len(logs) == 0 {
			t.Fatalf("%s 关键字 %v 无匹配: %v", c.dev, c.kws, err)
		}
	}
}
