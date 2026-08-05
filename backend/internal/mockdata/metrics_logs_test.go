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
