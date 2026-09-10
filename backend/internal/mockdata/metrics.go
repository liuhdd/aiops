package mockdata

import (
	"fmt"
	"sync"
	"time"

	"aiops/internal/model"
)

var (
	mu          sync.RWMutex
	metricCache map[string][]model.MetricPoint
	logCache    map[string][]model.LogEntry
	generated   bool
)

const stepMinutes = 5

// jitter 提供确定性的微小抖动，保证测试可重复
func jitter(minutesAgo int) float64 { return float64(minutesAgo % 5) }

// scenario returns a curve value for a device+metric at `minutesAgo` before now (0 = now)
func scenario(device, metric string, m int) float64 {
	switch {
	case device == "compute-01" && metric == "cpu_usage":
		if m <= 30 {
			return 85 + jitter(m)
		}
		return 55 + jitter(m)
	case device == "db-01" && metric == "disk_usage":
		return 70 + float64(120-m)*0.225
	case device == "compute-02" && metric == "gpu_util":
		if m <= 30 {
			return 0
		}
		return 50 + jitter(m)
	case device == "compute-02" && metric == "gpu_temp":
		if m <= 30 {
			return 92
		}
		return 60 + jitter(m)
	case device == "web-01" && metric == "mem_usage":
		if m <= 20 {
			return 93
		}
		return 72 + jitter(m)
	case device == "switch-core-01" && metric == "net_in":
		if m <= 20 {
			return 2 + jitter(m)
		}
		return 80 + jitter(m)
	case device == "db-01" && metric == "cpu_usage":
		if m <= 30 {
			return 90 + jitter(m)
		}
		return 55 + jitter(m)
	case device == "compute-01" && metric == "mem_usage":
		if m <= 20 {
			return 94
		}
		return 70 + jitter(m)
	case device == "web-01" && metric == "disk_usage":
		return 96
	case device == "storage-01" && metric == "disk_usage":
		return 96
	default:
		return 50 + jitter(m)
	}
}

func metricsFor(device string) []string {
	if device == "compute-01" || device == "compute-02" {
		return []string{"cpu_usage", "mem_usage", "disk_usage", "gpu_util", "gpu_temp"}
	}
	if device == "switch-core-01" {
		return []string{"net_in", "net_out"}
	}
	return []string{"cpu_usage", "mem_usage", "disk_usage"}
}

func GenerateAll(seed int64) {
	mu.Lock()
	defer mu.Unlock()
	if generated {
		return
	}
	generated = true
	_ = seed
	metricCache = map[string][]model.MetricPoint{}
	now := time.Now().Truncate(time.Minute)

	for _, d := range devices {
		for _, m := range metricsFor(d.Name) {
			var pts []model.MetricPoint
			for mnt := 120; mnt >= 0; mnt -= stepMinutes {
				pts = append(pts, model.MetricPoint{
					Time:  now.Add(-time.Duration(mnt) * time.Minute),
					Value: scenario(d.Name, m, mnt),
				})
			}
			metricCache[d.Name+"|"+m] = pts
		}
	}
	generateLogs(now)
}

type MetricPointView struct {
	Time  string  `json:"time"`
	Value float64 `json:"value"`
}

func ToMetricViews(pts []model.MetricPoint) []MetricPointView {
	out := make([]MetricPointView, 0, len(pts))
	for _, p := range pts {
		out = append(out, MetricPointView{Time: p.Time.Format("15:04"), Value: p.Value})
	}
	return out
}

func QueryMetric(device, metric string, from, to time.Time) ([]model.MetricPoint, error) {
	mu.RLock()
	defer mu.RUnlock()
	key := device + "|" + metric
	all, ok := metricCache[key]
	if !ok {
		return nil, fmt.Errorf("metric %s not found for device %s", metric, device)
	}
	var out []model.MetricPoint
	for _, p := range all {
		if (p.Time.Equal(from) || p.Time.After(from)) && (p.Time.Equal(to) || p.Time.Before(to)) {
			out = append(out, p)
		}
	}
	return out, nil
}
